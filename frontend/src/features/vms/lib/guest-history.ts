/**
 * How a guest's History entries read: their labels, one-line summaries, who
 * did them, and the outcome badge. JSX-free, so it is tested without a
 * renderer; the cells that show it are in guest-history-column-defs.tsx.
 */
import {
  deriveActor,
  formatAction,
} from "@/components/layout/activity-columns";
import {
  isOkExit,
  parseDetails,
  type DisplayStatus,
} from "@/components/layout/task-status";
import type {
  GuestHistoryCategory,
  GuestHistoryEntry,
  GuestHistorySource,
  GuestHistoryWindow,
} from "../api/guest-history-queries";

export const CATEGORY_LABEL: Record<GuestHistoryCategory, string> = {
  power: "Power",
  migration: "Migration",
  snapshot: "Snapshot",
  backup: "Backup",
  clone: "Clone",
  lifecycle: "Create / destroy",
  config: "Configuration",
  ha: "HA",
  console: "Console",
  other: "Other",
};

export const CATEGORY_OPTIONS: {
  value: GuestHistoryCategory | "";
  label: string;
}[] = [
  { value: "", label: "All types" },
  ...(Object.keys(CATEGORY_LABEL) as GuestHistoryCategory[]).map((c) => ({
    value: c,
    label: CATEGORY_LABEL[c],
  })),
];

export const SOURCE_LABEL: Record<GuestHistorySource, string> = {
  nexara: "Nexara",
  proxmox: "Proxmox",
  detected: "No task",
};

export const SOURCE_OPTIONS: {
  value: GuestHistorySource | "";
  label: string;
}[] = [
  { value: "", label: "All sources" },
  { value: "nexara", label: "Done in Nexara" },
  { value: "proxmox", label: "Done in Proxmox" },
  { value: "detected", label: "No task (seen by Nexara)" },
];

export const WINDOW_OPTIONS: { value: GuestHistoryWindow; label: string }[] = [
  { value: "", label: "All time" },
  { value: "24h", label: "Last 24 hours" },
  { value: "7d", label: "Last 7 days" },
  { value: "30d", label: "Last 30 days" },
  { value: "90d", label: "Last 90 days" },
];

/** The History table's columns. None sorts: the timeline is newest first. */
export type GuestHistoryColumnKey =
  "time" | "event" | "summary" | "who" | "node" | "outcome";

/**
 * What a History cell needs beyond the entry, resolved once per row so the
 * cells and the expanded row say the same thing.
 */
export interface GuestHistoryCellCtx {
  label: string;
  summary: string;
  actor: string;
  /** null: not a task, or its end was never recorded. */
  display: DisplayStatus | null;
  progress: number | null;
  expanded: boolean;
}

/** The Proxmox worker type a UPID names (field 5), or "". */
export function upidWorkerType(upid: string): string {
  return upid.split(":")[5] ?? "";
}

/**
 * What Proxmox's task types are called here. The Proxmox side of the history
 * is labelled by what Proxmox ran, which its own task list names this way.
 */
const TASK_TYPE_LABEL: Record<string, string> = {
  qmstart: "Start",
  vzstart: "Start",
  hastart: "Start (HA)",
  qmstop: "Stop",
  vzstop: "Stop",
  hastop: "Stop (HA)",
  qmshutdown: "Shutdown",
  vzshutdown: "Shutdown",
  qmreboot: "Reboot",
  vzreboot: "Reboot",
  qmreset: "Reset",
  qmsuspend: "Suspend",
  vzsuspend: "Suspend",
  qmpause: "Pause",
  qmresume: "Resume",
  vzresume: "Resume",
  qmigrate: "Migrate",
  vzmigrate: "Migrate",
  hamigrate: "Migrate (HA)",
  harelocate: "Relocate (HA)",
  qmsnapshot: "Take snapshot",
  vzsnapshot: "Take snapshot",
  qmdelsnapshot: "Delete snapshot",
  vzdelsnapshot: "Delete snapshot",
  qmrollback: "Roll back snapshot",
  vzrollback: "Roll back snapshot",
  vzdump: "Back up",
  qmrestore: "Restore",
  vzrestore: "Restore",
  qmclone: "Clone",
  vzclone: "Clone",
  qmcreate: "Create",
  vzcreate: "Create",
  qmdestroy: "Destroy",
  vzdestroy: "Destroy",
  qmtemplate: "Convert to template",
  vztemplate: "Convert to template",
  qmconfig: "Change config",
  qmmove: "Move disk",
  move_volume: "Move volume",
  resize: "Resize disk",
};

/** Nexara's own action names where the plain title-casing reads badly. */
const ACTION_LABEL: Record<string, string> = {
  drs_migrate_running: "DRS migration",
  drs_migrate: "DRS migration done",
  drs_migrate_failed: "DRS migration failed",
  drs_advisory: "DRS recommendation",
  migrate_running: "Migration",
  disk_move_running: "Disk move",
  console_token_mint: "Console opened",
  config_update: "Config change",
  set_pool: "Pool change",
  snapshot_create: "Take snapshot",
  snapshot_delete: "Delete snapshot",
  snapshot_rollback: "Roll back snapshot",
};

function statusChangeLabel(from: string, to: string): string {
  if (to === "unknown") return "Status unknown";
  if (from === "unknown") return `Reported ${to} again`;
  switch (to) {
    case "stopped":
      return "Stopped, no task";
    case "running":
      return "Started, no task";
    case "suspended":
      return "Suspended, no task";
  }
  return `Became ${to}, no task`;
}

/**
 * The Event column. A Nexara row is named by Nexara's action, which says more
 * than the Proxmox task behind it ("DRS migration", not "Migrate (HA)"); a
 * Proxmox row by the task Proxmox ran.
 */
export function historyEventLabel(
  entry: GuestHistoryEntry,
  pageVmid: number,
): string {
  if (entry.kind === "status") {
    return statusChangeLabel(entry.from_status ?? "", entry.to_status ?? "");
  }
  // A clone INTO this guest names its source: it is this guest's birth.
  if (entry.category === "clone" && entry.vmid !== pageVmid) {
    return `Cloned from VM ${String(entry.vmid)}`;
  }
  if (entry.resource_type === "ha_resource") {
    return `HA resource ${entry.action}`;
  }
  if (entry.kind === "audit" && entry.source === "nexara") {
    return ACTION_LABEL[entry.action] ?? formatAction(entry.action);
  }
  return (
    TASK_TYPE_LABEL[upidWorkerType(entry.upid)] ??
    ACTION_LABEL[entry.action] ??
    formatAction(entry.action)
  );
}

function str(d: Record<string, unknown>, key: string): string {
  const v = d[key];
  if (typeof v === "string") return v;
  if (typeof v === "number") return String(v);
  return "";
}

/**
 * The Summary column: the few details that say what this entry did — where a
 * migration went, which snapshot, which settings — rather than the whole
 * details blob, which the expanded row shows.
 */
export function historySummary(
  entry: GuestHistoryEntry,
  pageVmid: number,
): string {
  if (entry.kind === "status") {
    const from = entry.from_status ?? "";
    const to = entry.to_status ?? "";
    const where = entry.node ? ` on ${entry.node}` : "";
    if (to === "unknown") {
      return `Was ${from}${where}; the node stopped reporting the guest.`;
    }
    if (from === "unknown") {
      return `${to}${where} once the node reported the guest again.`;
    }
    // Only a stop has an everyday cause no task records.
    const why =
      to === "stopped"
        ? ": a shutdown or crash inside the guest, or the host"
        : "";
    return `${from} → ${to}${where}. No task explains it${why}.`;
  }
  const d = parseDetails(entry.details);
  const parts: string[] = [];
  const from = str(d, "source_node");
  const to = str(d, "target_node") || str(d, "target");
  if (from && to) parts.push(`${from} → ${to}`);
  else if (to) parts.push(`→ ${to}`);
  // The clone a guest was made from names it as new_id; the label says so.
  const newID = str(d, "new_id");
  if (newID && Number(newID) !== pageVmid) parts.push(`→ VM ${newID}`);
  const snap = str(d, "snap_name");
  if (snap) parts.push(snap);
  const fields = d["fields"];
  if (fields && typeof fields === "object") {
    const f = fields as Record<string, unknown>;
    const changed = Object.keys(f).filter((k) => k !== "delete");
    if (changed.length > 0) parts.push(changed.join(", "));
    const removed = typeof f["delete"] === "string" ? f["delete"] : "";
    if (removed) parts.push(`removed ${removed.split(",").join(", ")}`);
  }
  const disk = str(d, "disk");
  const storage = str(d, "target_storage") || str(d, "storage");
  if (disk) parts.push(storage ? `${disk} → ${storage}` : disk);
  if ("new_pool" in d) {
    parts.push(
      `pool ${str(d, "old_pool") || "none"} → ${str(d, "new_pool") || "none"}`,
    );
  }
  const volid = str(d, "volid");
  if (volid) parts.push(volid);
  const state = str(d, "state");
  if (entry.resource_type === "ha_resource" && state)
    parts.push(`state ${state}`);
  const reason = str(d, "reason");
  if (reason) parts.push(reason);
  const error = str(d, "error");
  if (error) parts.push(`Error: ${error}`);
  if (parts.length === 0 && entry.kind === "task") return entry.description;
  return parts.join(" · ");
}

/** Who did it — see deriveActor; "Proxmox" for an ingested task naming nobody. */
export function historyActor(entry: GuestHistoryEntry): string {
  if (entry.kind === "status") return "";
  const actor = deriveActor(entry, parseDetails(entry.details));
  if (actor === "System" && entry.source === "proxmox") return "Proxmox";
  return actor;
}

/**
 * The outcome badge, or null for an entry that is not a task or whose end was
 * never recorded. A live poll finishes a row the server still calls running —
 * the server's terminal outcome is never overridden (deriveTaskStatus's rule).
 */
export function historyDisplayStatus(
  entry: GuestHistoryEntry,
  live: { status: string; exit_status: string } | undefined,
): DisplayStatus | null {
  switch (entry.outcome) {
    case "ok":
      return "ok";
    case "failed":
      return "failed";
    case "running":
      if (live?.status === "stopped") {
        return isOkExit(live.exit_status) ? "ok" : "failed";
      }
      return "running";
  }
  return null;
}
