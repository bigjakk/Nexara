import { describe, it, expect } from "vitest";

import { deriveTaskStatus, isOkExit, parseDetails } from "./task-status";

describe("isOkExit", () => {
  it("treats empty, OK, and WARNINGS as success", () => {
    expect(isOkExit("")).toBe(true);
    expect(isOkExit("OK")).toBe(true);
    expect(isOkExit("WARNINGS: 3")).toBe(true);
  });
  it("treats anything else as failure", () => {
    expect(isOkExit("error: boom")).toBe(false);
  });
});

describe("deriveTaskStatus precedence", () => {
  const noDetails = {};

  it("prefers the live poll over server/details", () => {
    // live poll says running, even though server says completed
    expect(
      deriveTaskStatus({ task_status: "completed" }, noDetails, {
        status: "running",
        exitStatus: "",
      }),
    ).toBe("running");
    // live poll says stopped+error, even though server still says running
    expect(
      deriveTaskStatus({ task_status: "running" }, noDetails, {
        status: "stopped",
        exitStatus: "error",
      }),
    ).toBe("failed");
  });

  it("falls back to server task_status when not polling", () => {
    expect(deriveTaskStatus({ task_status: "running" }, noDetails, undefined)).toBe(
      "running",
    );
    expect(
      deriveTaskStatus({ task_status: "completed" }, noDetails, undefined),
    ).toBe("ok");
    expect(deriveTaskStatus({ task_status: "failed" }, noDetails, undefined)).toBe(
      "failed",
    );
  });

  it("classifies a raw 'stopped' status by exit status", () => {
    expect(
      deriveTaskStatus(
        { task_status: "stopped", task_exit_status: "OK" },
        noDetails,
        undefined,
      ),
    ).toBe("ok");
    expect(
      deriveTaskStatus(
        { task_status: "stopped", task_exit_status: "err: 1" },
        noDetails,
        undefined,
      ),
    ).toBe("failed");
  });

  it("falls back to external details.status for ingested tasks", () => {
    expect(deriveTaskStatus({}, { upid: "UPID:x", status: "OK" }, undefined)).toBe(
      "ok",
    );
    expect(
      deriveTaskStatus({}, { upid: "UPID:x", status: "err: 1" }, undefined),
    ).toBe("failed");
  });

  it("returns none for non-task or statusless entries", () => {
    expect(deriveTaskStatus({}, noDetails, undefined)).toBe("none");
    expect(deriveTaskStatus({}, { upid: "UPID:x" }, undefined)).toBe("none");
  });
});

describe("parseDetails", () => {
  it("parses valid JSON objects", () => {
    expect(parseDetails('{"upid":"u","vmid":110}')).toEqual({
      upid: "u",
      vmid: 110,
    });
  });
  it("returns {} for invalid or non-object JSON", () => {
    expect(parseDetails("not json")).toEqual({});
    expect(parseDetails("123")).toEqual({});
  });
});
