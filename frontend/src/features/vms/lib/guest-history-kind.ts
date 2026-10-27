/**
 * The guest kind of History: a VM's or container's (HistoryKind "guest" on
 * the server). What only a guest has is named here — its status changes no
 * task explains, its parts of backup jobs, the clone it was made from and its
 * HA resource; everything else reads as every kind's does
 * (features/history/lib/history.ts). Its strings are history-guest.json.
 */
import {
  Archive,
  ArrowLeftRight,
  Camera,
  Circle,
  Copy,
  Package,
  Power,
  Settings2,
  ShieldCheck,
  Terminal,
  type LucideIcon,
} from "lucide-react";
import type { TFunction } from "i18next";
import strings from "@/locales/en/history-guest.json";
import { historyKind } from "@/features/history/lib/history";
import type { HistoryEntry } from "@/features/history/types/history";

type GuestHistoryCategory = keyof typeof strings.categories;

const ICONS: Record<GuestHistoryCategory, LucideIcon> = {
  power: Power,
  migration: ArrowLeftRight,
  snapshot: Camera,
  backup: Archive,
  clone: Copy,
  lifecycle: Package,
  config: Settings2,
  ha: ShieldCheck,
  console: Terminal,
  other: Circle,
};

function statusChangeLabel(t: TFunction, from: string, to: string): string {
  if (to === "unknown") return t("events.statusUnknown");
  if (from === "unknown") return t("events.reportedAgain", { status: to });
  switch (to) {
    case "stopped":
      return t("events.stopped");
    case "running":
      return t("events.started");
    case "suspended":
      return t("events.suspended");
  }
  return t("events.became", { status: to });
}

function eventLabel(entry: HistoryEntry, t: TFunction): string | undefined {
  if (entry.kind === "status") {
    return statusChangeLabel(t, entry.from_status ?? "", entry.to_status ?? "");
  }
  if (entry.kind === "backup") return t("events.backupJob");
  // A clone INTO this guest names its source: it is this guest's birth.
  if (entry.category === "clone" && entry.vmid !== entry.subject_vmid) {
    return t("events.clonedFrom", { vmid: entry.vmid });
  }
  if (entry.resource_type === "ha_resource") {
    return t("events.haResource", { action: entry.action });
  }
  return undefined;
}

function summary(entry: HistoryEntry, t: TFunction): string | undefined {
  if (entry.kind === "status") {
    const from = entry.from_status ?? "";
    const to = entry.to_status ?? "";
    const onNode = entry.node ? t("summaries.onNode", { node: entry.node }) : "";
    if (to === "unknown") return t("summaries.unknown", { from, onNode });
    if (from === "unknown") return t("summaries.reportedAgain", { to, onNode });
    // Only a stop has an everyday cause no task records.
    return t(to === "stopped" ? "summaries.stopNoTask" : "summaries.noTask", {
      from,
      to,
      onNode,
    });
  }
  // Nexara's own description names the job it ran; Proxmox's says "backup".
  if (entry.kind === "backup") {
    return entry.source === "nexara" ? entry.description : "";
  }
  return undefined;
}

function note(entry: HistoryEntry, t: TFunction): string | undefined {
  if (entry.kind === "status") return t("notes.status");
  if (entry.kind === "backup") return t("notes.backup");
  return undefined;
}

export const guestHistoryKind = historyKind("guest", strings, {
  // The column widths a guest's History tab has kept since before the kinds.
  layoutKey: "guest-history",
  icons: ICONS,
  eventLabel,
  summary,
  note,
});
