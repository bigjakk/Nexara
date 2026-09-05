import { describe, it, expect } from "vitest";
import {
  DEFAULT_ACTIVITY_SORT,
  activityLabel,
  decorateActivity,
  deriveActor,
  userLabel,
  type LiveTaskStatus,
} from "./activity-columns";
import { SYSTEM_USER_ID } from "@/lib/constants";
import { ACTIVITY_COLUMN_DEFS } from "./activity-column-defs";
import { sortAccessorsFrom } from "@/hooks/useColumnLayout";
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

/** What the table actually sorts on — derived from the columns, the same way
 *  useDataTable does it for the panel. */
const ACCESSORS = sortAccessorsFrom(ACTIVITY_COLUMN_DEFS);

describe("ACTIVITY_COLUMN_DEFS", () => {
  it("hides User, Cluster and Progress below md, where the drawer has no room", () => {
    // Which three, specifically. The drawer is the narrowest table in the app,
    // so something has to go on a phone, and these are the columns whose loss
    // costs least: the cluster is usually one repeated value, progress is
    // already in the row's status, and the actor is usually the person holding
    // the phone. All three are still in the expanded row. Identity and time
    // are what the drawer is for and stay at every width.
    const hidden = ACTIVITY_COLUMN_DEFS.filter((c) => c.hideBelowMd).map(
      (c) => c.key,
    );
    expect(hidden).toEqual(["user", "cluster", "progress"]);
  });

  it("gives every column a width, so table-fixed has one to use", () => {
    for (const col of ACTIVITY_COLUMN_DEFS) {
      expect(col.width).toBeGreaterThan(0);
    }
  });

  it("gives every column a sort accessor", () => {
    // A column with neither sortValue nor sortable renders an inert heading —
    // it looks sortable and does nothing when clicked.
    for (const col of ACTIVITY_COLUMN_DEFS) {
      expect(col.sortValue).toBeTypeOf("function");
    }
  });

  it("puts Progress between Cluster and Time, with User after Action", () => {
    const keys = ACTIVITY_COLUMN_DEFS.map((c) => c.key);
    expect(keys.indexOf("user")).toBe(keys.indexOf("action") + 1);
    expect(keys.indexOf("cluster")).toBe(keys.indexOf("user") + 1);
    expect(keys.indexOf("progress")).toBe(keys.indexOf("cluster") + 1);
    expect(keys.indexOf("time")).toBe(keys.indexOf("progress") + 1);
  });

  it("opens newest-first, as the panel always has", () => {
    expect(DEFAULT_ACTIVITY_SORT).toEqual({ key: "time", direction: "desc" });
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

describe("activityLabel", () => {
  it("leaves no trailing dash on a row with no resource", () => {
    // What the task-progress dialog titles itself with. A login has no
    // resource at all, and the dialog used to compose this string without the
    // guard the Action column sorts through — so it read "Login — ".
    const row = decorateActivity(
      entry({ action: "login", resource_id: "" }),
      noLive,
    );
    expect(row.resourceLabel).toBe("");
    expect(activityLabel(row)).toBe("Login");
  });
});

describe("deriveActor", () => {
  it("credits an ingested Proxmox task to the PVE account that ran it", () => {
    // Every ingested row is written under the seeded system actor, so the
    // users join would credit a root@pam shutdown to "DRS Scheduler".
    const row = decorateActivity(
      entry({
        source: "proxmox",
        user_id: SYSTEM_USER_ID,
        user_display_name: "DRS Scheduler",
        details: JSON.stringify({ upid: "UPID:x", proxmox_user: "root@pam" }),
      }),
      noLive,
    );
    expect(row.actorLabel).toBe("root@pam");
  });

  it('calls a background action "System", not by the seeded display name', () => {
    // 000013 seeds the system user as "DRS Scheduler", which names one of the
    // four subsystems that write under it; a rolling update is not the DRS.
    const row = decorateActivity(
      entry({ user_id: SYSTEM_USER_ID, user_display_name: "DRS Scheduler" }),
      noLive,
    );
    expect(row.actorLabel).toBe("System");
  });

  it("prefers a signed-in user's display name over their email", () => {
    const row = decorateActivity(
      entry({ user_display_name: "Alice", user_email: "a@example.com" }),
      noLive,
    );
    expect(row.actorLabel).toBe("Alice");
  });

  it("falls back to the email when the display name is blank", () => {
    const row = decorateActivity(
      entry({ user_display_name: "", user_email: "a@example.com" }),
      noLive,
    );
    expect(row.actorLabel).toBe("a@example.com");
  });

  it("resolves to empty when nothing identifies an actor", () => {
    // A deleted user leaves the LEFT JOIN empty on both columns. The cell
    // shows an em dash for that rather than inventing one.
    expect(
      deriveActor({ user_id: "u1", user_display_name: "", user_email: "" }, {}),
    ).toBe("");
  });
});

describe("userLabel", () => {
  it("names the system actor for a row that is not an audit entry", () => {
    // The audit page's User filter lists AuditUserRefs, not entries. It has to
    // name its options by the same rule the rows are labelled with, or the
    // dropdown offers "DRS Scheduler" for the rows that read "System".
    expect(
      userLabel(SYSTEM_USER_ID, "DRS Scheduler", "system@nexara.local"),
    ).toBe("System");
  });

  it("leaves every other account alone", () => {
    expect(userLabel("u1", "Alice", "a@example.com")).toBe("Alice");
    expect(userLabel("u1", "", "a@example.com")).toBe("a@example.com");
  });
});

describe("activity sort accessors", () => {
  it("ranks status worst-first so an ascending click surfaces failures", () => {
    const rank = (task_status: string) =>
      ACCESSORS.status(
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
      ACCESSORS.status(decorateActivity(entry({ action: "login" }), noLive)),
    ).toBeNull();
  });

  it("ranks level by severity, not alphabetically", () => {
    // Alphabetically "error" < "info" < "warn" would bury warnings below info.
    const rank = (action: string) =>
      ACCESSORS.level(decorateActivity(entry({ action }), noLive));
    expect(rank("vm_delete_failed")).toBeLessThan(rank("vm_delete") as number);
    expect(rank("vm_delete")).toBeLessThan(rank("vm_start") as number);
  });

  it("sorts Action on the whole visible string, resource included", () => {
    const row = decorateActivity(
      entry({ action: "vm_start", resource_name: "web01", resource_vmid: 101 }),
      noLive,
    );
    expect(ACCESSORS.action(row)).toBe("Vm Start — web01 (101)");
  });

  it("sorts User on the resolved actor, not the raw join column", () => {
    // The cell shows the PVE account for an ingested task; ordering on
    // user_display_name would file that row under the system user instead.
    const row = decorateActivity(
      entry({
        source: "proxmox",
        user_id: SYSTEM_USER_ID,
        user_display_name: "DRS Scheduler",
        details: JSON.stringify({ upid: "UPID:x", proxmox_user: "root@pam" }),
      }),
      noLive,
    );
    expect(ACCESSORS.user(row)).toBe("root@pam");
  });

  it("treats an unattributable row as absent, matching its em dash", () => {
    expect(
      ACCESSORS.user(
        decorateActivity(
          entry({ user_display_name: "", user_email: "" }),
          noLive,
        ),
      ),
    ).toBeNull();
  });

  it("treats a blank cluster as absent, matching the em dash the cell shows", () => {
    expect(
      ACCESSORS.cluster(decorateActivity(entry({ cluster_name: "" }), noLive)),
    ).toBeNull();
  });

  it("sorts Time as epoch, not rendered text", () => {
    expect(
      ACCESSORS.time(
        decorateActivity(entry({ created_at: "2026-09-01T10:00:00Z" }), noLive),
      ),
    ).toBe(Date.parse("2026-09-01T10:00:00Z"));
  });
});
