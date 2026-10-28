/**
 * The node kind of History (HistoryKind "node" on the server): what happened
 * to one node — its own actions and its parts', the tasks that ran on it, its
 * status changes and its guests' arrivals and departures. A guest's row, when
 * the reader brings guest activity in, reads as on the guest's own History;
 * the node's status changes and moves are named here. Its strings are
 * history-node.json.
 */
import {
  Activity,
  BadgeCheck,
  Circle,
  Database,
  Download,
  HardDrive,
  ListChecks,
  Monitor,
  Network,
  Power,
  Settings2,
  Terminal,
  Wrench,
  type LucideIcon,
} from "lucide-react";
import type { TFunction } from "i18next";
import strings from "@/locales/en/history-node.json";
import { historyGuestVmid, historyKind } from "@/features/history/lib/history";
import type { HistoryEntry } from "@/features/history/types/history";
import { guestRowNaming } from "@/features/vms/lib/guest-history-kind";

type NodeHistoryCategory = keyof typeof strings.categories;

const ICONS: Record<NodeHistoryCategory, LucideIcon> = {
  status: Activity,
  power: Power,
  maintenance: Wrench,
  updates: Download,
  network: Network,
  storage: HardDrive,
  ceph: Database,
  certificates: BadgeCheck,
  options: Settings2,
  checks: ListChecks,
  console: Terminal,
  guests: Monitor,
  other: Circle,
};

const isNodeStatus = (entry: HistoryEntry) =>
  entry.kind === "status" && entry.resource_type === "node";

const isGuestRow = (entry: HistoryEntry) =>
  entry.kind !== "move" && !isNodeStatus(entry) && historyGuestVmid(entry) > 0;

/** A run: the changes or moves past twenty an hour, counted in one entry. */
const isRun = (entry: HistoryEntry) =>
  (isNodeStatus(entry) || entry.kind === "move") && (entry.run_count ?? 0) > 0;

function eventLabel(entry: HistoryEntry, t: TFunction): string | undefined {
  if (isRun(entry)) {
    return t(entry.kind === "move" ? "events.moveRun" : "events.statusRun", {
      count: entry.run_count,
    });
  }
  if (isNodeStatus(entry)) {
    const to = entry.to_status ?? "";
    if (to === "unknown") return t("events.statusUnknown");
    if (entry.from_status === "unknown") {
      return t("events.reportedAgain", { status: to });
    }
    return t("events.statusChanged", { status: to });
  }
  if (entry.kind === "move") {
    if (entry.action === "now_on") {
      return t("events.nowOn", { vmid: entry.vmid, node: entry.to_status });
    }
    return t(entry.action === "arrived" ? "events.arrived" : "events.left");
  }
  return isGuestRow(entry) ? guestRowNaming.eventLabel(entry) : undefined;
}

function summary(entry: HistoryEntry, t: TFunction): string | undefined {
  if (isRun(entry)) {
    return t("summaries.run", {
      from: entry.from_status ?? "",
      to: entry.to_status ?? "",
    });
  }
  if (isNodeStatus(entry)) {
    const from = entry.from_status ?? "";
    if (entry.to_status === "unknown") return t("summaries.unknown", { from });
    return t("summaries.status", { from, to: entry.to_status ?? "" });
  }
  if (entry.kind === "move") {
    if (entry.action === "now_on") {
      return t("summaries.nowOn", {
        from: entry.from_status,
        to: entry.to_status,
      });
    }
    return t(
      entry.action === "arrived" ? "summaries.arrived" : "summaries.left",
      {
        node: entry.node,
      },
    );
  }
  return isGuestRow(entry) ? guestRowNaming.summary(entry) : undefined;
}

function note(entry: HistoryEntry, t: TFunction): string | undefined {
  if (isRun(entry)) return t("notes.run");
  if (isNodeStatus(entry)) return t("notes.status");
  if (entry.kind === "move") {
    return t(entry.action === "now_on" ? "notes.nowOn" : "notes.move");
  }
  return isGuestRow(entry) ? guestRowNaming.note(entry) : undefined;
}

export const nodeHistoryKind = historyKind("node", strings, {
  icons: ICONS,
  // A busy node's trail is long: the default is its last week, without its
  // daily checks or its guests' activity, which the reader brings back.
  defaultWindow: "7d",
  routine: ["checks"],
  guestToggle: true,
  eventLabel,
  summary,
  note,
});
