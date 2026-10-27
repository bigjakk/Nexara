import type { HistoryEntry } from "@/features/history/types/history";

/**
 * A History entry. Shared rather than feature-local: every kind's tests build
 * entries, and kinds disagreeing about what one looks like is how a label test
 * passes against a shape the timeline never sees.
 *
 * Defaults describe a Nexara audit row with no task, filed under VM 100.
 */
export function makeHistoryEntry(
  over: Partial<HistoryEntry> = {},
): HistoryEntry {
  return {
    id: "audit:1",
    kind: "audit",
    occurred_at: "2026-01-01T00:00:00Z",
    category: "other",
    source: "nexara",
    action: "",
    resource_type: "vm",
    resource_id: "",
    vmid: 100,
    subject_vmid: 100,
    user_id: "u1",
    user_email: "ops@example.com",
    user_display_name: "Ops",
    details: "{}",
    upid: "",
    node: "pve-01",
    description: "",
    task_type: "",
    outcome: "",
    exit_status: "",
    finished_at: null,
    ...over,
  };
}
