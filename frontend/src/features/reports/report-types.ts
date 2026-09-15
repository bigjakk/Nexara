import type { ReportRun, ReportType } from "@/types/api";

/** A parameter a report type honours; the fields component renders each. */
export type ReportParamKey =
  "stale_after_hours" | "top_n" | "snapshot_warn_days";

export interface ReportTypeInfo {
  value: ReportType;
  label: string;
  /** One sentence on what the report answers. */
  blurb: string;
  /** What is inside, in the order the report shows it. */
  sections: string[];
  /** Whether the period matters: point-in-time reports use it as context only. */
  windowed: boolean;
  params: ReportParamKey[];
  /** Sections a schedule may switch off, by the key the generator checks. */
  optionalSections: { key: string; label: string }[];
}

/**
 * The catalogue, in the order the Reports page shows it. Must agree with
 * reports.AllTypes on the server and the report_schedules CHECK constraint.
 */
export const REPORT_TYPES: readonly ReportTypeInfo[] = [
  {
    value: "cluster_digest",
    label: "Cluster digest",
    blurb:
      "One page across availability, load, backups, alerts, tasks, snapshots and patching.",
    sections: [
      "Findings from every other report",
      "Cluster load per day and node status",
      "Backup coverage",
      "Alerts and tasks in the period",
      "Snapshot and patch figures",
    ],
    windowed: true,
    params: ["stale_after_hours", "snapshot_warn_days"],
    optionalSections: [],
  },
  {
    value: "backup_compliance",
    label: "Backup compliance",
    blurb:
      "Every guest against Proxmox Backup Server and Veeam: coverage, runs, repository headroom, findings.",
    sections: [
      "Coverage by state and recovery-point age",
      "Backup runs, jobs and failures in the period",
      "Datastore and repository capacity with days-to-full",
      "Every guest, problems first",
      "Veeam malware verdicts and orphaned objects",
    ],
    windowed: true,
    params: ["stale_after_hours"],
    optionalSections: [{ key: "runs", label: "Backup runs in the period" }],
  },
  {
    value: "resource_utilization",
    label: "Resource utilisation",
    blurb:
      "CPU, memory, disk and network per node and per day, with sustained-pressure findings.",
    sections: ["Cluster daily trend", "Per-node averages and peaks"],
    windowed: true,
    params: [],
    optionalSections: [],
  },
  {
    value: "vm_resource_usage",
    label: "VM resource usage",
    blurb:
      "Top consumers of CPU, memory, network and disk, plus the guests that sat idle.",
    sections: [
      "Top consumers per resource, as bars and tables",
      "Idle guests",
      "Every guest with its period averages",
    ],
    windowed: true,
    params: ["top_n"],
    optionalSections: [],
  },
  {
    value: "capacity_forecast",
    label: "Capacity forecast",
    blurb:
      "Where the period's trend takes CPU and memory per node, and how full each storage pool is.",
    sections: ["Node forecasts to 100 %", "Storage pool fill"],
    windowed: true,
    params: [],
    optionalSections: [],
  },
  {
    value: "snapshot_inventory",
    label: "Snapshot inventory",
    blurb: "Every guest snapshot by age, and the ones most likely forgotten.",
    sections: ["Snapshot age distribution", "Oldest snapshots"],
    windowed: false,
    params: ["snapshot_warn_days"],
    optionalSections: [],
  },
  {
    value: "patch_status",
    label: "Patch status",
    blurb:
      "The latest vulnerability scan per node, known-exploited CVEs first.",
    sections: ["Vulnerabilities per node", "Known exploited"],
    windowed: false,
    params: [],
    optionalSections: [],
  },
  {
    value: "uptime_summary",
    label: "Uptime summary",
    blurb:
      "Which nodes are up, for how long, and which rebooted in the period.",
    sections: ["Nodes online now", "Reboots inside the period"],
    windowed: true,
    params: [],
    optionalSections: [],
  },
];

export const REPORT_TYPE_LABELS: Record<string, string> = Object.fromEntries(
  REPORT_TYPES.map((t) => [t.value, t.label]),
);

export function reportTypeInfo(value: string): ReportTypeInfo | undefined {
  return REPORT_TYPES.find((t) => t.value === value);
}

export function reportTypeLabel(value: string): string {
  return REPORT_TYPE_LABELS[value] ?? value;
}

/** Human period for a run or schedule: "7 days" or "36 h". */
export function periodLabel(hours: number): string {
  if (hours >= 24 && hours % 24 === 0) {
    const days = hours / 24;
    return days === 1 ? "24 hours" : `${String(days)} days`;
  }
  return `${String(hours)} h`;
}

/** File stem for downloads: type, cluster and the run's date. */
export function runFileName(
  run: ReportRun,
  clusterName: string,
  ext: string,
): string {
  const stem =
    `${run.report_type}-${clusterName}-${run.created_at.slice(0, 10)}`
      .toLowerCase()
      .replace(/[^a-z0-9._-]+/g, "-");
  return `${stem}.${ext}`;
}
