import { describe, it, expect } from "vitest";
import i18n from "@/lib/i18n";
import { makeHistoryEntry } from "@/test/history.fixtures";
import {
  historyEventLabel,
  historyNamespace,
  historySummary,
} from "@/features/history/lib/history";
import { folderHistoryKind } from "./folder-history-kind";

const F = "f0000000-0000-4000-8000-000000000001";
const G = "f0000000-0000-4000-8000-000000000002";
const GONE = "f0000000-0000-4000-8000-000000000009";
const names = new Map([
  [F, "folder01"],
  [G, "folder02"],
]);
const kind = folderHistoryKind((id) => names.get(id));
const t = i18n.getFixedT(null, historyNamespace(kind));

const membership = (action: string, details: object) =>
  makeHistoryEntry({
    resource_type: "vm_folder_membership",
    action,
    subject_vmid: 0,
    details: JSON.stringify(details),
  });
const folder = (action: string, details: object, id = F) =>
  makeHistoryEntry({
    resource_type: "vm_folder",
    resource_id: id,
    action,
    vmid: 0,
    subject_vmid: 0,
    details: JSON.stringify(details),
  });

describe("the folder kind's own rows", () => {
  // Folders are named as the page lists them now, a deleted one by what its
  // row recorded; a guest in no folder is in Discovered.
  it.each([
    [
      "a guest filed",
      membership("assign", { vmid: 100, folder_id: F }),
      "Filed in a folder",
      "→ folder01",
    ],
    [
      "a guest moved",
      membership("assign", { vmid: 100, folder_id: G, from_folder_id: F }),
      "Moved between folders",
      "folder01 → folder02",
    ],
    [
      "a guest unfiled",
      membership("unassign", {
        vmid: 100,
        folder_id: null,
        from_folder_id: F,
      }),
      "Removed from its folder",
      "folder01 → Discovered",
    ],
    [
      "an unfiling that predates from_folder_id",
      membership("unassign", { vmid: 100, folder_id: null }),
      "Removed from its folder",
      "→ Discovered",
    ],
    [
      "a move into a folder since deleted",
      membership("assign", { vmid: 100, folder_id: GONE, from_folder_id: F }),
      "Moved between folders",
      "folder01 → a deleted folder",
    ],
    [
      "a folder created",
      folder("create", { name: "folder03", parent_id: null }),
      "Folder created",
      "folder03",
    ],
    [
      "a folder renamed",
      folder("update", { name: "folder04" }),
      "Folder renamed",
      "→ folder04",
    ],
    [
      "a folder moved",
      folder("update", { parent_id: G, from_parent_id: null }),
      "Folder moved",
      "folder01: Top level → folder02",
    ],
    [
      "a folder deleted",
      folder("delete", { name: "folder05", parent_id: F }, GONE),
      "Folder deleted",
      "folder05",
    ],
  ])("%s", (_name, entry, label, summary) => {
    expect(historyEventLabel(kind, entry, t)).toBe(label);
    expect(historySummary(kind, entry, t)).toBe(summary);
  });

  // A guest's row reads as on its own History, in the guest kind's strings.
  it("names a guest's row as the guest's History does", () => {
    const stop = makeHistoryEntry({
      kind: "status",
      source: "detected",
      action: "status_change",
      from_status: "running",
      to_status: "stopped",
    });
    expect(historyEventLabel(kind, stop, t)).toBe("Stopped, no task");
  });
});
