import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import type { TaskRecord } from "../api/tasks-queries";
import { TASK_COLUMNS } from "./task-column-defs";
import type { TaskCellCtx } from "./task-columns";

// A task filed through the API is marked with who filed it, as an observed
// Proxmox task is marked PVE.
describe("task columns", () => {
  it("marks a filed task's description with who filed it", () => {
    const task: TaskRecord = {
      id: "t1",
      cluster_id: "c1",
      upid: "UPID:pve-01:1:2:3:qmstart:101:alice@pve:",
      description: "",
      status: "running",
      exit_status: "",
      node: "pve-01",
      task_type: "qmstart",
      source: "filed",
      filed_by: "Alice",
      progress: null,
      started_at: "2026-11-16T10:00:00Z",
      finished_at: null,
    };
    const ctx: TaskCellCtx = {
      clusterName: "cluster01",
      vmName: "",
      display: "running",
      progress: null,
      expanded: false,
    };
    const description = TASK_COLUMNS.find((c) => c.key === "description");
    render(<>{description?.cell(task, ctx)}</>);
    expect(screen.getByText("Filed by Alice")).toBeInTheDocument();
  });
});
