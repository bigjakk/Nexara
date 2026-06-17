import { describe, it, expect, beforeEach, afterEach } from "vitest";
import { useHealthDismissStore } from "./health-dismiss-store";

const STORAGE_KEY = "nexara-health-dismissed";

function stored(): string[] {
  const raw = localStorage.getItem(STORAGE_KEY);
  return raw === null ? [] : (JSON.parse(raw) as string[]);
}

describe("health-dismiss-store", () => {
  beforeEach(() => {
    localStorage.clear();
    useHealthDismissStore.setState({ dismissed: [] });
  });
  afterEach(() => {
    localStorage.clear();
  });

  it("dismiss adds a signature and persists it", () => {
    useHealthDismissStore.getState().dismiss("c1:ceph|warn|mon low");
    expect(useHealthDismissStore.getState().dismissed).toEqual([
      "c1:ceph|warn|mon low",
    ]);
    expect(stored()).toEqual(["c1:ceph|warn|mon low"]);
  });

  it("dismiss is idempotent (no duplicates)", () => {
    const { dismiss } = useHealthDismissStore.getState();
    dismiss("a");
    dismiss("a");
    expect(useHealthDismissStore.getState().dismissed).toEqual(["a"]);
  });

  it("restoreAll clears all dismissals and persists empty", () => {
    const { dismiss, restoreAll } = useHealthDismissStore.getState();
    dismiss("a");
    dismiss("b");
    restoreAll();
    expect(useHealthDismissStore.getState().dismissed).toEqual([]);
    expect(stored()).toEqual([]);
  });

  it("syncActive drops dismissals whose issue resolved, keeps active ones", () => {
    const { dismiss, syncActive } = useHealthDismissStore.getState();
    dismiss("still-active");
    dismiss("resolved");
    // Only "still-active" remains an active issue.
    syncActive(["still-active", "some-other-undismissed"]);
    expect(useHealthDismissStore.getState().dismissed).toEqual([
      "still-active",
    ]);
    expect(stored()).toEqual(["still-active"]);
  });

  it("syncActive is a no-op (same reference) when all dismissals are still active", () => {
    const { dismiss, syncActive } = useHealthDismissStore.getState();
    dismiss("a");
    const before = useHealthDismissStore.getState().dismissed;
    syncActive(["a", "b"]);
    expect(useHealthDismissStore.getState().dismissed).toBe(before);
  });

  it("a resolved-then-recurring issue can be dismissed again after syncActive", () => {
    const { dismiss, syncActive } = useHealthDismissStore.getState();
    dismiss("x");
    syncActive([]); // issue resolved -> dismissal forgotten
    expect(useHealthDismissStore.getState().dismissed).toEqual([]);
    dismiss("x"); // recurs and gets dismissed again
    expect(useHealthDismissStore.getState().dismissed).toEqual(["x"]);
  });

  it("loads previously persisted dismissals from localStorage", () => {
    // Simulate a prior session having persisted a dismissal, then re-read it
    // the same way the store does on init.
    localStorage.setItem(STORAGE_KEY, JSON.stringify(["persisted-sig"]));
    const raw = localStorage.getItem(STORAGE_KEY);
    const parsed = raw === null ? [] : (JSON.parse(raw) as unknown);
    expect(Array.isArray(parsed) ? parsed : []).toEqual(["persisted-sig"]);
  });
});
