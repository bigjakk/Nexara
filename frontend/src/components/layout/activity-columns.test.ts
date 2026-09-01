import { describe, it, expect } from "vitest";
import {
  ACTIVITY_ACCESSORS,
  ACTIVITY_COLUMNS,
  DEFAULT_ACTIVITY_SORT,
  activityColumnVisibility,
  decorateActivity,
  type LiveTaskStatus,
} from "./activity-columns";
import type { AuditLogEntry } from "@/features/audit/api/audit-queries";

function entry(over: Partial<AuditLogEntry>): AuditLogEntry {
  return {
    id: "a1",
    cluster_id: "c1",
    user_id: "u1",
    resource_type: "vm",
    resource_id: "r1",
    action: "vm_start",
    details: "{}",
    created_at: "2026-09-01T10:00:00Z",
    source: "nexara",
    user_email: "u@example.com",
    user_display_name: "U",
    cluster_name: "ceph-prod",
    resource_vmid: 0,
    resource_name: "",
    ...over,
  };
}

const noLive: Record<string, LiveTaskStatus> = {};

describe("ACTIVITY_COLUMNS", () => {
  it("puts Progress between Cluster and Time", () => {
    const keys = ACTIVITY_COLUMNS.map((c) => c.key);
    expect(keys.indexOf("progress")).toBe(keys.indexOf("cluster") + 1);
    expect(keys.indexOf("time")).toBe(keys.indexOf("progress") + 1);
  });

  it("opens newest-first, as the panel always has", () => {
    expect(DEFAULT_ACTIVITY_SORT).toEqual({ key: "time", direction: "desc" });
  });

  it("has an accessor for every column", () => {
    for (const col of ACTIVITY_COLUMNS) {
      expect(ACTIVITY_ACCESSORS[col.key]).toBeTypeOf("function");
    }
  });
});

describe("decorateActivity", () => {
  it("gives a non-task entry no progress at all", () => {
    // A login has no outcome — distinct from a task whose progress Proxmox
    // never reported, and it must not draw a bar.
    const row = decorateActivity(entry({ action: "login" }), noLive);
    expect(row.status).toBe("none");
    expect(row.progress).toBeNull();
  });

  it("shows a completed task as full even with no stored progress", () => {
    const row = decorateActivity(
      entry({
        details: JSON.stringify({ upid: "UPID:x" }),
        task_status: "completed",
      }),
      noLive,
    );
    expect(row.progress).toBe(1);
  });

  it("prefers the live poll's fraction for a running task", () => {
    const row = decorateActivity(
      entry({
        details: JSON.stringify({ upid: "UPID:x" }),
        task_status: "running",
        task_progress: 0.2,
      }),
      { "UPID:x": { status: "running", exitStatus: "", progress: 0.62 } },
    );
    expect(row.progress).toBe(0.62);
  });

  it("falls back to the server's fraction before any live poll arrives", () => {
    const row = decorateActivity(
      entry({
        details: JSON.stringify({ upid: "UPID:x" }),
        task_status: "running",
        task_progress: 0.2,
      }),
      noLive,
    );
    expect(row.progress).toBe(0.2);
  });

  it("escalates severity to error when the task failed", () => {
    // The action name says nothing alarming; the task outcome does.
    const row = decorateActivity(
      entry({
        action: "vm_start",
        details: JSON.stringify({ upid: "UPID:x" }),
        task_status: "failed",
      }),
      noLive,
    );
    expect(row.severity).toBe("error");
  });
});

describe("ACTIVITY_ACCESSORS", () => {
  it("ranks status worst-first so an ascending click surfaces failures", () => {
    const rank = (task_status: string) =>
      ACTIVITY_ACCESSORS.status(
        decorateActivity(
          entry({ details: JSON.stringify({ upid: "U" }), task_status }),
          noLive,
        ),
      );
    expect(rank("failed")).toBeLessThan(rank("completed") as number);
    expect(rank("completed")).toBeLessThan(rank("running") as number);
  });

  it("ranks a non-task entry as null so it never leads either direction", () => {
    expect(
      ACTIVITY_ACCESSORS.status(decorateActivity(entry({ action: "login" }), noLive)),
    ).toBeNull();
  });

  it("ranks level by severity, not alphabetically", () => {
    // Alphabetically "error" < "info" < "warn" would bury warnings below info.
    const rank = (action: string) =>
      ACTIVITY_ACCESSORS.level(decorateActivity(entry({ action }), noLive));
    expect(rank("vm_delete_failed")).toBeLessThan(rank("vm_delete") as number);
    expect(rank("vm_delete")).toBeLessThan(rank("vm_start") as number);
  });

  it("sorts Action on the whole visible string, resource included", () => {
    const row = decorateActivity(
      entry({ action: "vm_start", resource_name: "web01", resource_vmid: 101 }),
      noLive,
    );
    expect(ACTIVITY_ACCESSORS.action(row)).toBe("Vm Start — web01 (101)");
  });

  it("treats a blank cluster as absent, matching the em dash the cell shows", () => {
    expect(
      ACTIVITY_ACCESSORS.cluster(decorateActivity(entry({ cluster_name: "" }), noLive)),
    ).toBeNull();
  });

  it("sorts Time as epoch, not rendered text", () => {
    expect(
      ACTIVITY_ACCESSORS.time(
        decorateActivity(entry({ created_at: "2026-09-01T10:00:00Z" }), noLive),
      ),
    ).toBe(Date.parse("2026-09-01T10:00:00Z"));
  });
});

describe("activityColumnVisibility", () => {
  it("yields Cluster and Progress below md, where the drawer has no room", () => {
    expect(activityColumnVisibility("cluster")).toBe("hidden md:table-cell");
    expect(activityColumnVisibility("progress")).toBe("hidden md:table-cell");
  });

  it("keeps the rest visible at every width", () => {
    for (const key of ["status", "level", "action", "time"] as const) {
      expect(activityColumnVisibility(key)).toBe("");
    }
  });

  it("carries no visibility class in the column definitions themselves", () => {
    // Header and cell both read activityColumnVisibility(); a stray `hidden`
    // baked into className would apply to the header only and shift every
    // following cell under the wrong heading.
    for (const col of ACTIVITY_COLUMNS) {
      expect(col.className).not.toContain("hidden");
    }
  });
});
