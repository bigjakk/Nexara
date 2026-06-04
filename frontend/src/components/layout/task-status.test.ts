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

  it("lets a terminal server status win over a stale poll (regression)", () => {
    // Regression: a poller's leftover "running" must NOT override a completed
    // server status — this was the "stuck on running after finish" bug.
    expect(
      deriveTaskStatus({ task_status: "completed" }, noDetails, {
        status: "running",
        exitStatus: "",
      }),
    ).toBe("ok");
    expect(
      deriveTaskStatus({ task_status: "failed" }, noDetails, {
        status: "running",
        exitStatus: "",
      }),
    ).toBe("failed");
  });

  it("uses the live poll to flip a still-running task to done sooner", () => {
    // server still says running, but the poll knows it stopped — flip early
    expect(
      deriveTaskStatus({ task_status: "running" }, noDetails, {
        status: "stopped",
        exitStatus: "error",
      }),
    ).toBe("failed");
    expect(
      deriveTaskStatus({ task_status: "running" }, noDetails, {
        status: "stopped",
        exitStatus: "OK",
      }),
    ).toBe("ok");
    expect(
      deriveTaskStatus({ task_status: "running" }, noDetails, {
        status: "running",
        exitStatus: "",
      }),
    ).toBe("running");
  });

  it("uses the live poll as a fallback when there is no server status", () => {
    // ingested external task with no task_history row
    expect(
      deriveTaskStatus({}, { upid: "UPID:x" }, { status: "stopped", exitStatus: "OK" }),
    ).toBe("ok");
    expect(
      deriveTaskStatus({}, { upid: "UPID:x" }, { status: "running", exitStatus: "" }),
    ).toBe("running");
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
