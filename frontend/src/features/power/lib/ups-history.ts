/**
 * A UPS's History (the HistoryKind "ups-device" on the server,
 * internal/api/registry_ups_history.go): its power events, named and timed as
 * the Power events tab names them (ups-state.ts), the commands run and
 * variables set on it, and the changes to what it powers. Its strings are
 * history-ups-device.json; what every kind shares is history.ts's.
 */
import type { TFunction } from "i18next";
import { Circle, Plug, Terminal, Zap, type LucideIcon } from "lucide-react";
import { parseDetails } from "@/components/layout/task-status";
import type { ClusterHistory } from "@/features/history/lib/cluster-histories";
import { historyKind } from "@/features/history/lib/history";
import type { HistoryEntry } from "@/features/history/types/history";
import { apiPath } from "@/lib/api-path";
import { formatDateTime } from "@/lib/format";
import strings from "@/locales/en/history-ups-device.json";
import type { UpsEvent } from "../types/ups";
import { eventDuration, eventEnded, eventLabel, isUpsEventKind } from "./ups-state";

const ICONS: Readonly<Record<string, LucideIcon>> = {
  power: Zap,
  commands: Terminal,
  feeds: Plug,
  other: Circle,
};

/** A power entry as the Power events tab's helpers read an event: its start is
 *  the entry's time, its end and readings are in details. */
function powerEvent(
  entry: HistoryEntry,
): Pick<UpsEvent, "kind" | "started_at" | "ended_at" | "end_observed" | "details"> | undefined {
  if (entry.kind !== "power" || !isUpsEventKind(entry.action)) return undefined;
  const d = parseDetails(entry.details);
  const details: UpsEvent["details"] = {};
  if (typeof d["result"] === "string") details.result = d["result"];
  if (typeof d["status"] === "string") details.status = d["status"];
  if (d["ended_by"] === "polling_off" || d["ended_by"] === "removed") details.ended_by = d["ended_by"];
  return {
    kind: entry.action,
    started_at: entry.occurred_at,
    ended_at: typeof d["ended_at"] === "string" ? d["ended_at"] : null,
    end_observed: d["end_observed"] !== false,
    details,
  };
}

const COMMAND_EVENTS = {
  command: { ups_command_sent: "events.command", ups_command_failed: "events.commandFailed" },
  set: { ups_command_sent: "events.set", ups_command_failed: "events.setFailed" },
} as const;

/** The Event column: a power event as the Power events tab names it, and a
 *  command or setting by what was run or set. */
export function upsHistoryEventLabel(entry: HistoryEntry, t: TFunction): string | undefined {
  const event = powerEvent(entry);
  if (event !== undefined) return eventLabel(event);
  if (entry.action !== "ups_command_sent" && entry.action !== "ups_command_failed") return undefined;
  const d = parseDetails(entry.details);
  const name = typeof d["name"] === "string" ? d["name"] : "";
  if (name === "") return undefined;
  return t(COMMAND_EVENTS[d["kind"] === "set" ? "set" : "command"][entry.action], { name });
}

/** The Summary column: how long a power event lasted and what the battery got
 *  down to; a command's risk and why it failed; the node or label a feed names. */
export function upsHistorySummary(entry: HistoryEntry, t: TFunction): string | undefined {
  const d = parseDetails(entry.details);
  const parts: string[] = [];
  const event = powerEvent(entry);
  if (event !== undefined) {
    if (event.kind !== "self_test") parts.push(eventDuration(event, Date.now()));
    if (typeof d["lowest_charge"] === "number") {
      parts.push(t("summaries.lowestCharge", { value: Math.round(d["lowest_charge"]) }));
    }
    if (typeof d["peak_load"] === "number") parts.push(t("summaries.peakLoad", { value: Math.round(d["peak_load"]) }));
  } else if (entry.action.startsWith("ups_command_")) {
    if (typeof d["tier"] === "string" && d["tier"] !== "everyday") parts.push(t(`tiers.${d["tier"]}`));
    if (typeof d["error"] === "string") parts.push(t("summaries.error", { error: d["error"] }));
  } else if (entry.action.startsWith("ups_feed_")) {
    const node = typeof d["node"] === "string" ? d["node"] : "";
    const label = typeof d["label"] === "string" ? d["label"] : "";
    parts.push(node !== "" ? node : label);
    if (typeof d["protected_elsewhere"] === "boolean") {
      parts.push(t(d["protected_elsewhere"] ? "summaries.protectedElsewhere" : "summaries.checkedByNexara"));
    }
  } else {
    return undefined;
  }
  return parts.filter((p) => p !== "").join(" · ");
}

/** The note under an expanded power event: when it ended, in local time and as
 *  the Power events tab says it, since its details hold the end in UTC. */
export function upsHistoryNote(entry: HistoryEntry, t: TFunction): string | undefined {
  const event = powerEvent(entry);
  if (event === undefined || event.kind === "self_test") return undefined;
  return t("notes.ended", { when: eventEnded(event, event.ended_at === null ? "" : formatDateTime(event.ended_at)) });
}

export const upsHistoryKind = historyKind("ups-device", strings, {
  icons: ICONS,
  // The server's window for a read that names none (handlers.WithObjectWindow).
  defaultWindow: "7d",
  eventLabel: upsHistoryEventLabel,
  summary: upsHistorySummary,
  note: upsHistoryNote,
});

/** A UPS's History. Global: a UPS is read through a NUT server no one cluster
 *  owns, so it takes global view:audit and global view:ups. */
export function upsHistory(upsId: string): ClusterHistory {
  return {
    kind: upsHistoryKind,
    permission: "ups",
    target: {
      clusterId: null,
      path: (query) => apiPath`/api/v1/ups-devices/${upsId}/history?${query}`,
    },
  };
}
