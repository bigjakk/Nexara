import { describe, it, expect } from "vitest";
import { deriveDisplayStatus } from "./task-columns";
import { TASK_COLUMNS, TASK_COLUMNS_WITH_VM } from "./task-column-defs";
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

describe("task columns", () => {
  it("drops the VM column outside guest-scoped views", () => {
    expect(TASK_COLUMNS.map((c) => c.key)).not.toContain("vm");
    expect(TASK_COLUMNS_WITH_VM.map((c) => c.key)).toContain("vm");
  });

  it("differs from the guest-scoped set by exactly the VM column", () => {
    expect(TASK_COLUMNS_WITH_VM.length).toBe(TASK_COLUMNS.length + 1);
  });

  it("puts Progress between Node and Status", () => {
    const keys = TASK_COLUMNS.map((c) => c.key);
    expect(keys.indexOf("progress")).toBe(keys.indexOf("node") + 1);
    expect(keys.indexOf("status")).toBe(keys.indexOf("progress") + 1);
  });

  it("gives every column a width, so table-fixed has one to use", () => {
    // A column with no width renders at zero under `table-layout: fixed`,
    // which reads as a missing column rather than a mis-sized one.
    for (const col of TASK_COLUMNS_WITH_VM) {
      expect(col.width).toBeGreaterThan(0);
    }
  });

  it("is sortable without a client accessor — this table sorts server-side", () => {
    // Both halves matter. `sortable` is what puts the control on the header at
    // all; without it every heading in this table would be inert. No
    // sortValue, because a client accessor would re-sort the delivered page on
    // top of the server's ordering — the bug server-side sorting exists to
    // avoid.
    for (const col of TASK_COLUMNS_WITH_VM) {
      expect(col.sortable).toBe(true);
      expect(col.sortValue).toBeUndefined();
    }
  });
});

describe("deriveDisplayStatus", () => {
  it("classifies a stopped task by its exit status", () => {
    expect(
      deriveDisplayStatus(
        task({ status: "stopped", exit_status: "OK" }),
        undefined,
      ),
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

  it("classifies an unrecognised status by its exit status, not as a non-task", () => {
    // deriveTaskStatus answers "none" here — it also serves non-task audit
    // entries. Every task_history row is a task, so this falls back to the
    // exit status, the same rule sort_status applies in queries/tasks.sql.
    expect(
      deriveDisplayStatus(task({ status: "", exit_status: "OK" }), undefined),
    ).toBe("ok");
    expect(
      deriveDisplayStatus(
        task({ status: "", exit_status: "got timeout" }),
        undefined,
      ),
    ).toBe("failed");
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
