import { describe, it, expect } from "vitest";
import {
  deriveDisplayStatus,
  taskColumnCount,
  taskColumns,
} from "./task-columns";
import type { TaskRecord } from "../api/tasks-queries";

function task(over: Partial<TaskRecord>): TaskRecord {
  return {
    id: "t1",
    cluster_id: "c1",
    upid: "UPID:x",
    description: "",
    status: "running",
    exit_status: "",
    node: "",
    task_type: "",
    source: "nexara",
    progress: null,
    started_at: "2026-09-01T00:00:00Z",
    finished_at: null,
    ...over,
  };
}

describe("taskColumns", () => {
  it("drops the VM column outside guest-scoped views", () => {
    expect(taskColumns(false).map((c) => c.key)).not.toContain("vm");
    expect(taskColumns(true).map((c) => c.key)).toContain("vm");
  });

  it("keeps colSpan in step with the rendered columns", () => {
    // The expanded detail row and the empty state span the table; a count that
    // drifts from the header leaves a ragged row rather than a full-width one.
    expect(taskColumnCount(false)).toBe(taskColumns(false).length);
    expect(taskColumnCount(true)).toBe(taskColumns(false).length + 1);
  });

  it("puts Progress between Node and Status", () => {
    const keys = taskColumns(false).map((c) => c.key);
    expect(keys.indexOf("progress")).toBe(keys.indexOf("node") + 1);
    expect(keys.indexOf("status")).toBe(keys.indexOf("progress") + 1);
  });
});

describe("deriveDisplayStatus", () => {
  it("classifies a stopped task by its exit status", () => {
    expect(
      deriveDisplayStatus(task({ status: "stopped", exit_status: "OK" }), undefined),
    ).toBe("ok");
    expect(
      deriveDisplayStatus(
        task({ status: "stopped", exit_status: "WARNINGS: 3" }),
        undefined,
      ),
    ).toBe("ok");
    expect(
      deriveDisplayStatus(
        task({ status: "stopped", exit_status: "unable to open" }),
        undefined,
      ),
    ).toBe("failed");
  });

  it("lets a live poll finish a row the server still calls running", () => {
    expect(
      deriveDisplayStatus(task({ status: "running" }), {
        status: "stopped",
        exit_status: "OK",
      }),
    ).toBe("ok");
  });

  it("never lets a stale live poll reopen a terminal row", () => {
    // The d86b7df fix: a poller cache still saying "running" must not override
    // a server status that has already gone terminal.
    expect(
      deriveDisplayStatus(task({ status: "completed" }), {
        status: "running",
        exit_status: "",
      }),
    ).toBe("ok");
  });
});
