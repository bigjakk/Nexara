/**
 * The Histories of storage and backup: a storage's, a Proxmox Backup Server's
 * and a datastore's, a Veeam server's, a job's and a run's, and a Proxmox
 * backup job's (the HistoryKinds "storage", "pbs", "veeam-server", … on the
 * server, internal/api/registry_storage_history.go and its siblings). A
 * placement is a kind and where its route is; a tab shows one, a list row's
 * button another:
 *
 *     <HistoryTabContent history={backupHistories.storage(clusterId, name)} />
 *     <HistoryButton {...backupHistories.veeamJob(serverId, jobId)} title={name} />
 *
 * Their strings are history-<name>.json. What every kind shares — the filters,
 * the columns, the naming — is history.ts's; here is only what these rows say
 * of themselves, from their details.
 */
import {
  Archive,
  ArchiveRestore,
  CalendarClock,
  Camera,
  Circle,
  FileText,
  HardDrive,
  Link2,
  ListChecks,
  Package,
  Plug,
  Play,
  Server,
  Settings2,
  Square,
  Wrench,
  type LucideIcon,
} from "lucide-react";
import type { TFunction } from "i18next";
import { apiPath } from "@/lib/api-path";
import { parseDetails } from "@/components/layout/task-status";
import backupJobStrings from "@/locales/en/history-backup-job.json";
import pbsStrings from "@/locales/en/history-pbs.json";
import storageStrings from "@/locales/en/history-storage.json";
import veeamJobStrings from "@/locales/en/history-veeam-job.json";
import veeamServerStrings from "@/locales/en/history-veeam-server.json";
import veeamSessionStrings from "@/locales/en/history-veeam-session.json";
import type { HistoryEntry, HistoryKind } from "../types/history";
import type { ClusterHistory } from "./cluster-histories";
import { historyKind } from "./history";

/** Every category any of these kinds has, with its icon; the timeline draws a
 *  plain circle for a category without one. */
const ICONS: Readonly<Record<string, LucideIcon>> = {
  // A storage's
  lifecycle: Package,
  config: Settings2,
  content: FileText,
  disks: HardDrive,
  backup: Archive,
  other: Circle,
  // A PBS server's
  server: Server,
  snapshots: Camera,
  maintenance: Wrench,
  jobs: ListChecks,
  restore: ArchiveRestore,
  // A Veeam server's, job's and run's
  connection: Plug,
  mapping: Link2,
  runs: Play,
  schedule: CalendarClock,
  stop: Square,
};

/** A detail the row holds as text, or "". */
function str(d: Record<string, unknown>, key: string): string {
  const v = d[key];
  if (typeof v === "string") return v;
  if (typeof v === "number") return String(v);
  return "";
}

/** The first of keys the row holds. */
function firstOf(d: Record<string, unknown>, keys: readonly string[]): string {
  for (const key of keys) {
    const v = str(d, key);
    if (v !== "") return v;
  }
  return "";
}

/** What went wrong, when the row says. */
function errorOf(d: Record<string, unknown>, t: TFunction): string {
  const error = str(d, "error");
  return error === "" ? "" : t("summaries.error", { error });
}

function joined(parts: readonly string[]): string | undefined {
  const kept = parts.filter((part) => part !== "");
  return kept.length > 0 ? kept.join(" · ") : undefined;
}

/**
 * A storage's Summary: the file, the volume or the disk a row is about — a
 * disk move says where the disk was and where it went — and, for the rows of a
 * guest or a restore, which guest.
 */
function storageSummary(entry: HistoryEntry, t: TFunction): string | undefined {
  if (entry.kind !== "audit") return undefined;
  const d = parseDetails(entry.details);
  const disk = str(d, "disk");
  const from = str(d, "from_storage");
  const to = firstOf(d, ["target_storage", "storage"]);
  let moved = disk;
  if (disk !== "" && to !== "") {
    moved = from === "" ? `${disk} → ${to}` : `${disk}: ${from} → ${to}`;
  }
  const guest = str(d, "vmid") || (entry.vmid > 0 ? String(entry.vmid) : "");
  return joined([
    moved,
    firstOf(d, ["volume", "filename", "reference", "template"]),
    entry.resource_type !== "storage" && guest !== ""
      ? t("summaries.guest", { vmid: guest })
      : "",
    errorOf(d, t),
  ]);
}

/**
 * A PBS server's Summary: the datastore, job or snapshot a row is about, and
 * where a restore went.
 */
function pbsSummary(entry: HistoryEntry, t: TFunction): string | undefined {
  if (entry.kind !== "audit") return undefined;
  const d = parseDetails(entry.details);
  const type = str(d, "backup_type");
  const id = str(d, "backup_id");
  const restoredTo = entry.resource_type === "backup" ? str(d, "vmid") : "";
  return joined([
    firstOf(d, ["datastore", "store"]),
    str(d, "job_id"),
    type !== "" && id !== "" ? `${type}/${id}` : "",
    restoredTo === "" ? "" : t("summaries.restoredTo", { vmid: restoredTo }),
    errorOf(d, t),
  ]);
}

/**
 * A Veeam row's Summary: the job or run it is about, and, of a mapping, what
 * was mapped to what — a platform to a cluster, a backup object to a guest.
 */
function veeamSummary(entry: HistoryEntry, t: TFunction): string | undefined {
  if (entry.kind !== "audit") return undefined;
  const d = parseDetails(entry.details);
  const platform = str(d, "platform");
  const cluster = str(d, "cluster_name");
  const object = str(d, "object");
  const guest = str(d, "guest_name");
  return joined([
    platform !== "" && cluster !== "" ? `${platform} → ${cluster}` : platform,
    object !== "" && guest !== "" ? `${object} → ${guest}` : object,
    firstOf(d, ["job_name", "session_name"]),
    str(d, "reason"),
    errorOf(d, t),
  ]);
}

/** A trigger the Proxmox Backup Server ran under a task of its own. */
function pbsNote(entry: HistoryEntry, t: TFunction): string | undefined {
  return entry.kind === "audit" &&
    str(parseDetails(entry.details), "pbs_upid") !== ""
    ? t("notes.pbsTask")
    : undefined;
}

/** The window every one of these Histories opens on: the server's default for
 *  a request that names none (handlers.WithObjectWindow), so All time asks for
 *  all_time. */
const WINDOW = "7d";

const kinds = {
  storage: historyKind("storage", storageStrings, {
    icons: ICONS,
    defaultWindow: WINDOW,
    summary: storageSummary,
  }),
  pbs: historyKind("pbs", pbsStrings, {
    icons: ICONS,
    defaultWindow: WINDOW,
    summary: pbsSummary,
    note: pbsNote,
  }),
  veeamServer: historyKind("veeam-server", veeamServerStrings, {
    icons: ICONS,
    defaultWindow: WINDOW,
    summary: veeamSummary,
  }),
  veeamJob: historyKind("veeam-job", veeamJobStrings, {
    icons: ICONS,
    defaultWindow: WINDOW,
    summary: veeamSummary,
  }),
  veeamSession: historyKind("veeam-session", veeamSessionStrings, {
    icons: ICONS,
    defaultWindow: WINDOW,
    summary: veeamSummary,
  }),
  backupJob: historyKind("backup-job", backupJobStrings, {
    icons: ICONS,
    defaultWindow: WINDOW,
  }),
} as const satisfies Record<string, HistoryKind>;

export const backupHistoryKinds = kinds;

/**
 * A kind, where to read it, and the view permission its object's own page
 * needs beside the audit log, as the cluster page's placements are
 * (ClusterHistory): offered only to a caller who holds it, so nothing offered
 * answers 403 for want of it (the server holds the scope: a backup server's is
 * instance-wide).
 */
export type BackupHistory = ClusterHistory;

/**
 * Where each History is read. An id is one path segment, which apiPath
 * encodes. A storage is its name, as Proxmox calls it, and a Veeam job or run
 * Veeam's own id for it: the rows of a History outlive the inventory row each
 * of them is shown from, which is replaced under a new id.
 */
export const backupHistories = {
  /** A storage's. A storage that is not shared is a different one on each
   *  node: with its node, only what ran there. */
  storage: (
    clusterId: string,
    storage: string,
    node?: string,
  ): BackupHistory => ({
    kind: kinds.storage,
    permission: "storage",
    target: {
      clusterId,
      path: (query) => {
        if (node !== undefined && node !== "") query.set("node", node);
        return apiPath`/api/v1/clusters/${clusterId}/storages/${storage}/history?${query}`;
      },
    },
  }),
  /** A PBS server's, or one of its datastores'. A global History: it is read
   *  in every cluster, and the server may belong to none. */
  pbsServer: (pbsId: string, datastore?: string): BackupHistory => ({
    kind: kinds.pbs,
    permission: "pbs",
    target: {
      clusterId: null,
      path: (query) => {
        if (datastore !== undefined && datastore !== "") {
          query.set("datastore", datastore);
        }
        return apiPath`/api/v1/pbs-servers/${pbsId}/history?${query}`;
      },
    },
  }),
  veeamServer: (serverId: string): BackupHistory => ({
    kind: kinds.veeamServer,
    permission: "veeam",
    target: {
      clusterId: null,
      path: (query) =>
        apiPath`/api/v1/veeam-servers/${serverId}/history?${query}`,
    },
  }),
  veeamJob: (serverId: string, jobId: string): BackupHistory => ({
    kind: kinds.veeamJob,
    permission: "veeam",
    target: {
      clusterId: null,
      path: (query) =>
        apiPath`/api/v1/veeam-servers/${serverId}/jobs/${jobId}/history?${query}`,
    },
  }),
  veeamSession: (serverId: string, sessionId: string): BackupHistory => ({
    kind: kinds.veeamSession,
    permission: "veeam",
    target: {
      clusterId: null,
      path: (query) =>
        apiPath`/api/v1/veeam-servers/${serverId}/sessions/${sessionId}/history?${query}`,
    },
  }),
  backupJob: (clusterId: string, jobId: string): BackupHistory => ({
    kind: kinds.backupJob,
    permission: "backup",
    target: {
      clusterId,
      path: (query) =>
        apiPath`/api/v1/clusters/${clusterId}/backup-jobs/${jobId}/history?${query}`,
    },
  }),
} as const;
