import { describe, it, expect } from "vitest";
import { issueSig, isIssueHidden, visibleClusterIssues } from "./health-issues";
import type { HealthIssue } from "@/types/api";

const mk = (over: Partial<HealthIssue> = {}): HealthIssue => ({
  type: "x",
  severity: "warn",
  scope: "cluster",
  target: "",
  summary: "S",
  detail: "D",
  ...over,
});

describe("health-issues", () => {
  it("issueSig is stable, cluster-scoped, and state-sensitive", () => {
    const i = mk();
    expect(issueSig("c1", i)).toBe(issueSig("c1", i));
    expect(issueSig("c1", i)).not.toBe(issueSig("c2", i)); // different cluster
    expect(issueSig("c1", i)).not.toBe(issueSig("c1", mk({ severity: "err" })));
    expect(issueSig("c1", i)).not.toBe(issueSig("c1", mk({ detail: "other" })));
  });

  it("hides dismissed signatures (only for the matching cluster)", () => {
    const i = mk({ type: "ceph" });
    const dismissed = new Set([issueSig("c1", i)]);
    expect(isIssueHidden("c1", i, dismissed, new Set())).toBe(true);
    expect(isIssueHidden("c2", i, dismissed, new Set())).toBe(false);
    expect(visibleClusterIssues("c1", [i], dismissed, new Set())).toEqual([]);
    expect(visibleClusterIssues("c2", [i], dismissed, new Set())).toEqual([i]);
  });

  it("hides muted types across clusters", () => {
    const task = mk({ type: "task_failed" });
    const ceph = mk({ type: "ceph" });
    const muted = new Set(["task_failed"]);
    expect(visibleClusterIssues("c1", [task, ceph], new Set(), muted)).toEqual([
      ceph,
    ]);
    expect(visibleClusterIssues("c2", [task], new Set(), muted)).toEqual([]);
  });
});
