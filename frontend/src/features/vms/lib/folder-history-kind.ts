/**
 * The folder kind of History (HistoryKind "folder" on the server): what
 * happened to a folder's guests and to the folders themselves. A guest's row
 * reads exactly as on the guest's own History, in the guest kind's strings;
 * only the folders' rows — a folder made, renamed, moved or deleted, a guest
 * filed into or out of one — are named here. Its strings are
 * history-folder.json.
 */
import { FolderOpen } from "lucide-react";
import type { TFunction } from "i18next";
import strings from "@/locales/en/history-folder.json";
import { parseDetails } from "@/components/layout/task-status";
import { historyKind } from "@/features/history/lib/history";
import type {
  HistoryEntry,
  HistoryKind,
} from "@/features/history/types/history";
import { guestHistoryKind, guestRowNaming } from "./guest-history-kind";

function isFolderRow(entry: HistoryEntry): boolean {
  return (
    entry.resource_type === "vm_folder" ||
    entry.resource_type === "vm_folder_membership"
  );
}

function eventLabel(entry: HistoryEntry, t: TFunction): string {
  const d = parseDetails(entry.details);
  if (entry.resource_type === "vm_folder") {
    switch (entry.action) {
      case "create":
        return t("events.folderCreated");
      case "delete":
        return t("events.folderDeleted");
    }
    return t("parent_id" in d ? "events.folderMoved" : "events.folderRenamed");
  }
  if (entry.action === "unassign") return t("events.unfiled");
  return t("from_folder_id" in d ? "events.moved" : "events.filed");
}

/**
 * Builds the kind over the page's folders: folderName names a folder by its
 * id, where the page still lists it. A folder it no longer lists is named by
 * the row itself where the row says, else "a deleted folder".
 */
export function folderHistoryKind(
  folderName: (id: string) => string | undefined,
): HistoryKind {
  // A folder an id names; null is the top level of the tree, or Discovered
  // for a guest.
  const named = (id: unknown, t: TFunction, none: string): string => {
    if (typeof id !== "string" || id === "") return t(none);
    return folderName(id) ?? t("summaries.deletedFolder");
  };
  const summary = (entry: HistoryEntry, t: TFunction): string => {
    const d = parseDetails(entry.details);
    if (entry.resource_type === "vm_folder") {
      // The name it had then, where the row says: a create, a delete, a
      // rename's new one. A move names it as it is now.
      const then = typeof d["name"] === "string" ? d["name"] : undefined;
      const self = named(entry.resource_id, t, "summaries.deletedFolder");
      if (entry.action === "update" && "parent_id" in d) {
        return t("summaries.named", {
          name: self,
          from: named(d["from_parent_id"], t, "summaries.topLevel"),
          to: named(d["parent_id"], t, "summaries.topLevel"),
        });
      }
      if (entry.action === "update") {
        return t("summaries.into", { to: then ?? self });
      }
      return then ?? self;
    }
    const to = named(d["folder_id"], t, "summaries.discovered");
    if (!("from_folder_id" in d)) return t("summaries.into", { to });
    return t("summaries.path", {
      from: named(d["from_folder_id"], t, "summaries.discovered"),
      to,
    });
  };
  return historyKind("folder", strings, {
    icons: { ...guestHistoryKind.icons, folder: FolderOpen },
    // A folder holds many guests, and so many guests' histories.
    defaultWindow: "7d",
    eventLabel: (entry, t) =>
      isFolderRow(entry)
        ? eventLabel(entry, t)
        : guestRowNaming.eventLabel(entry),
    summary: (entry, t) =>
      isFolderRow(entry) ? summary(entry, t) : guestRowNaming.summary(entry),
    note: (entry) =>
      isFolderRow(entry) ? undefined : guestRowNaming.note(entry),
  });
}
