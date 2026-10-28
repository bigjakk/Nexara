import { describe, it, expect } from "vitest";
import i18n from "@/lib/i18n";
import { makeHistoryEntry } from "@/test/history.fixtures";
import {
  historyEventLabel,
  historyNamespace,
  historySummary,
} from "@/features/history/lib/history";
import { nodeHistoryKind } from "./node-history-kind";

const t = i18n.getFixedT(null, historyNamespace(nodeHistoryKind));

const status = (from: string, to: string) =>
  makeHistoryEntry({
    kind: "status",
    source: "detected",
    action: "status_change",
    resource_type: "node",
    resource_id: "pve-01",
    vmid: 0,
    subject_vmid: 0,
    from_status: from,
    to_status: to,
  });
const move = (action: string, node: string, from = "", to = "") =>
  makeHistoryEntry({
    kind: "move",
    source: "detected",
    action,
    resource_type: "vm",
    vmid: 101,
    subject_vmid: 0,
    node,
    from_status: from,
    to_status: to,
  });

describe("the node kind's own rows", () => {
  it.each([
    [
      "went offline",
      status("online", "offline"),
      "Went offline",
      "online → offline",
    ],
    [
      "stopped reporting",
      status("online", "unknown"),
      "Stopped reporting",
      "Was online; Proxmox stopped reporting the node.",
    ],
    [
      "reported again",
      status("unknown", "online"),
      "Reported online again",
      "unknown → online",
    ],
    [
      "a guest arrived",
      move("arrived", "pve-02"),
      "Guest arrived",
      "From pve-02",
    ],
    ["a guest left", move("left", "pve-03"), "Guest left", "To pve-03"],
    [
      "a VMID seen elsewhere, no migration on record",
      move("now_on", "pve-02", "pve-01", "pve-02"),
      "VMID 101 now on pve-02",
      "pve-01 → pve-02, no migration on record",
    ],
  ])("%s", (_name, entry, label, summary) => {
    expect(historyEventLabel(nodeHistoryKind, entry, t)).toBe(label);
    expect(historySummary(nodeHistoryKind, entry, t)).toBe(summary);
  });

  // A guest's row reads as on its own History, in the guest kind's strings.
  it("names a guest's row as the guest's History does", () => {
    const drs = makeHistoryEntry({
      action: "drs_migrate_running",
      vmid: 101,
      subject_vmid: 0,
    });
    expect(historyEventLabel(nodeHistoryKind, drs, t)).toBe("DRS migration");
  });
});
