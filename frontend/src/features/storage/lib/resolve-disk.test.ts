import { afterEach, describe, expect, it, vi } from "vitest";
import { apiClient } from "@/lib/api-client";
import {
  ctVolumeKey,
  TASK_UNREADABLE_MS,
  vmVolumeKey,
  waitForTask,
  type TaskOutcome,
} from "./resolve-disk";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

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

// A status read that fails (a restart, a proxy's 502) is no failed move: the
// move may still hold the guest's lock. The wait retries, and only a status
// unread for TASK_UNREADABLE_MS is "unknown".
describe("waitForTask", () => {
  afterEach(() => {
    vi.useRealTimers();
  });
  const UPID = "UPID:pve-01:00000001:00000001:00000001:qmmove:100:alice@pve:";

  // "blip": one good read, still running, resets the run of failures, so
  // two short outages more than two minutes apart are not "unknown".
  it.each([
    ["reads the end once the status is readable again", "recovers"],
    ["says unknown once the status stays unreadable", "fails"],
    ["counts the two minutes from the last good read", "blip"],
  ] as const)("%s", async (_name, then) => {
    vi.useFakeTimers();
    vi.mocked(apiClient.get).mockRejectedValue(new Error("502"));
    let outcome: TaskOutcome | undefined;
    void waitForTask(
      "c1",
      UPID,
      () => false,
      new AbortController().signal,
    ).then((o) => {
      outcome = o;
    });

    await vi.advanceTimersByTimeAsync(TASK_UNREADABLE_MS - 31_000);
    expect(outcome).toBeUndefined();
    if (then === "recovers") {
      vi.mocked(apiClient.get).mockResolvedValue({
        status: "stopped",
        exit_status: "OK",
      });
    } else if (then === "blip") {
      vi.mocked(apiClient.get).mockResolvedValueOnce({
        status: "running",
        exit_status: "",
      });
    }
    await vi.advanceTimersByTimeAsync(61_000);

    expect(outcome).toEqual(
      {
        recovers: { status: "ended", ok: true, exit: "OK" },
        fails: { status: "unknown" },
        blip: undefined,
      }[then],
    );
  });
});
