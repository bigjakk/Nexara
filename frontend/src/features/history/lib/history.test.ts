/// <reference types="vite/client" />
import { describe, it, expect } from "vitest";
import { resources } from "@/lib/i18n";
import { makeHistoryEntry as entry } from "@/test/history.fixtures";
import { historyActor, historyDisplayStatus } from "./history";

// A kind's strings — and the shared timeline's — reach it only once
// lib/i18n.ts registers them; unregistered, every label shows its key and
// nothing fails.
it("registers every History namespace", () => {
  const files = Object.keys(
    import.meta.glob([
      "/src/locales/en/history.json",
      "/src/locales/en/history-*.json",
    ]),
  );
  expect(files.length).toBeGreaterThan(1);
  for (const file of files) {
    const ns = file.slice(file.lastIndexOf("/") + 1, -".json".length);
    expect(Object.keys(resources.en)).toContain(ns);
  }
});

const SYSTEM = "00000000-0000-0000-0000-000000000001";

describe("historyActor", () => {
  it.each([
    [
      {
        source: "proxmox" as const,
        user_id: SYSTEM,
        details: JSON.stringify({ proxmox_user: "root@pam" }),
      },
      "root@pam",
    ],
    [
      { kind: "task" as const, source: "proxmox" as const, user_id: SYSTEM },
      "Proxmox",
    ],
    [{ user_id: SYSTEM }, "System"],
    [{}, "Ops"],
    [{ kind: "status" as const }, ""],
  ])("credits %o to %s", (over, want) => {
    expect(historyActor(entry(over))).toBe(want);
  });
});

describe("historyDisplayStatus", () => {
  it.each([
    ["ok", undefined, "ok"],
    ["failed", undefined, "failed"],
    ["", undefined, null],
    ["running", undefined, "running"],
    // A poll finishes a row the server still calls running…
    ["running", { status: "stopped", exit_status: "WARNINGS: 1" }, "ok"],
    ["running", { status: "stopped", exit_status: "unable to lock" }, "failed"],
    // …and never overrides the server's own verdict.
    ["ok", { status: "running", exit_status: "" }, "ok"],
  ] as const)("outcome %s with poll %o shows %s", (outcome, live, want) => {
    expect(historyDisplayStatus(entry({ outcome }), live)).toBe(want);
  });
});
