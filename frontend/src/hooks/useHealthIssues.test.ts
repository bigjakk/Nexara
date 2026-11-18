import { describe, expect, it } from "vitest";
import type { HealthIssue } from "@/types/api";
import { issueLink } from "./useHealthIssues";

const issue = (fields: Partial<HealthIssue>): HealthIssue => ({
  type: "node_offline",
  severity: "warn",
  scope: "cluster",
  target: "",
  summary: "",
  detail: "",
  ...fields,
});

describe("issueLink", () => {
  it.each([
    [
      "a store's issue",
      {
        type: "storage_inactive",
        scope: "storage" as const,
        target: "store01",
      },
      "/clusters/c1?tab=storage",
    ],
    ["Ceph's", { type: "ceph" }, "/clusters/c1?tab=ceph"],
    ["an HA error", { type: "ha_error" }, "/clusters/c1?tab=ha"],
    [
      "anything else",
      { type: "node_offline", scope: "node" as const },
      "/clusters/c1",
    ],
  ])("opens %s at %j", (_label, fields, want) => {
    expect(issueLink("c1", issue(fields))).toBe(want);
  });
});
