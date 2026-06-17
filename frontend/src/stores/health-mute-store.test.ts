import { describe, it, expect, beforeEach, afterEach } from "vitest";
import { useHealthMuteStore } from "./health-mute-store";

const STORAGE_KEY = "nexara-health-muted-types";

function stored(): string[] {
  const raw = localStorage.getItem(STORAGE_KEY);
  return raw === null ? [] : (JSON.parse(raw) as string[]);
}

describe("health-mute-store", () => {
  beforeEach(() => {
    localStorage.clear();
    useHealthMuteStore.setState({ mutedTypes: [] });
  });
  afterEach(() => {
    localStorage.clear();
  });

  it("mute adds a type and persists it", () => {
    useHealthMuteStore.getState().mute("task_failed");
    expect(useHealthMuteStore.getState().mutedTypes).toEqual(["task_failed"]);
    expect(stored()).toEqual(["task_failed"]);
  });

  it("mute is idempotent", () => {
    const { mute } = useHealthMuteStore.getState();
    mute("x");
    mute("x");
    expect(useHealthMuteStore.getState().mutedTypes).toEqual(["x"]);
  });

  it("unmute removes a single type, leaving others", () => {
    const { mute, unmute } = useHealthMuteStore.getState();
    mute("a");
    mute("b");
    unmute("a");
    expect(useHealthMuteStore.getState().mutedTypes).toEqual(["b"]);
    expect(stored()).toEqual(["b"]);
  });

  it("restoreAll clears every muted type", () => {
    const { mute, restoreAll } = useHealthMuteStore.getState();
    mute("a");
    mute("b");
    restoreAll();
    expect(useHealthMuteStore.getState().mutedTypes).toEqual([]);
    expect(stored()).toEqual([]);
  });
});
