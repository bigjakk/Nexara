import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ADMIN } from "@/test/fake-server";
import type { ConsoleTarget } from "../types/console";
import { consoleIdentity, leaveLaunch, takeLaunch } from "./console-launch";

const target: ConsoleTarget = {
  clusterID: "cluster01",
  type: "vm_vnc",
  node: "pve-01",
  vmid: 101,
  label: "",
};
const identity = consoleIdentity(target);

beforeEach(() => {
  vi.useFakeTimers();
});

afterEach(() => {
  vi.useRealTimers();
  localStorage.clear();
});

// Who a launch may be for is the page's test (ConsoleSessionPage.test.tsx).
describe("takeLaunch", () => {
  it.each<[string, (id: string) => string, string, number]>([
    ["another console", (id) => `#launch=${id}`, "cluster01|vm_vnc|102", 0],
    ["past its time", (id) => `#launch=${id}`, identity, 5 * 60_000 + 1],
    ["an id that is not one", () => "#launch=../../console", identity, 0],
  ])("refuses a launch for %s", (_, hash, console, elapsed) => {
    const id = leaveLaunch(target, ADMIN.id) ?? "";
    vi.advanceTimersByTime(elapsed);

    expect(takeLaunch(hash(id), ADMIN.id, console)).toBe(false);
  });

  it("takes it out of storage, and leaving the next drops the ones nobody took", () => {
    const taken = leaveLaunch(target, ADMIN.id) ?? "";
    const left = leaveLaunch(target, ADMIN.id) ?? "";
    expect(takeLaunch(`#launch=${taken}`, ADMIN.id, identity)).toBe(true);
    vi.advanceTimersByTime(5 * 60_000 + 1);

    leaveLaunch(target, ADMIN.id);

    const keys = Object.keys(localStorage);
    expect(keys).toHaveLength(1);
    expect(keys.some((k) => k.endsWith(taken) || k.endsWith(left))).toBe(false);
  });
});
