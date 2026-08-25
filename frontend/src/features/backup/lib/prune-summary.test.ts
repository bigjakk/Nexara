import { describe, it, expect } from "vitest";
import { summarizePrune } from "./prune-summary";
import type { PBSPruneJob } from "../types/backup";

const STORE = "Test-Backup-Datastore";

function job(over: Partial<PBSPruneJob> = {}): PBSPruneJob {
  return { id: "j1", store: STORE, schedule: "daily", ...over };
}

const settled = { loading: false, failed: false };

// summarizePrune is where every judgement lives, so it is tested directly.
// The card's job is only to render what it returns.
describe("summarizePrune", () => {
  it("reports the job's schedule when the datastore carries none", () => {
    const s = summarizePrune(undefined, {}, [job({ "keep-daily": 14 })], settled);
    expect(s.scheduleLabel).toBe("daily (prune job)");
    expect(s.retention?.["keep-daily"]).toBe(14);
  });

  it("prefers the datastore's own schedule where it exists (older PBS)", () => {
    const s = summarizePrune("weekly", {}, [job()], settled);
    expect(s.scheduleLabel).toBe("weekly");
  });

  it("says Not configured only when nothing prunes the datastore", () => {
    expect(summarizePrune(undefined, {}, [], settled).scheduleLabel).toBe(
      "Not configured",
    );
  });

  // Each of these three used to collapse into "Not configured" — a confident
  // claim built from something other than "we looked and there is nothing".
  it("distinguishes a disabled job from no job", () => {
    const s = summarizePrune(undefined, {}, [job({ disable: true })], settled);
    expect(s.scheduleLabel).toBe("Disabled");
  });

  it("does not claim anything while the jobs are still loading", () => {
    const s = summarizePrune(undefined, {}, undefined, { loading: true, failed: false });
    expect(s.scheduleLabel).toBe("Loading…");
  });

  it("does not claim anything when the jobs could not be read", () => {
    const s = summarizePrune(undefined, {}, undefined, { loading: false, failed: true });
    expect(s.scheduleLabel).toMatch(/unavailable/i);
    expect(s.scheduleLabel).not.toMatch(/not configured/i);
  });

  // A namespace-scoped job prunes part of the datastore, so presenting it as
  // datastore-wide is the same over-claim in the other direction.
  it("names a namespace-scoped job as scoped", () => {
    const s = summarizePrune(undefined, {}, [job({ ns: "prod" })], settled);
    expect(s.scheduleLabel).toBe("daily (prune job, namespace prod)");
  });

  it("flags a partially namespace-scoped set", () => {
    const s = summarizePrune(
      undefined,
      {},
      [job({ id: "a", ns: "prod" }), job({ id: "b" })],
      settled,
    );
    expect(s.scheduleLabel).toMatch(/some namespace-scoped/);
  });

  it("refuses to present one job's retention as the datastore's when they differ", () => {
    const s = summarizePrune(
      undefined,
      {},
      [job({ id: "a", "keep-daily": 14 }), job({ id: "b", "keep-daily": 30 })],
      settled,
    );
    expect(s.scheduleLabel).toContain("2 jobs");
    expect(s.retention).toBeNull();
    expect(s.retentionNote).toMatch(/varies across 2 jobs/);
  });

  it("shows shared retention when the jobs agree", () => {
    const s = summarizePrune(
      undefined,
      {},
      [job({ id: "a", "keep-daily": 14 }), job({ id: "b", "keep-daily": 14 })],
      settled,
    );
    expect(s.retention?.["keep-daily"]).toBe(14);
    expect(s.retentionNote).toBeUndefined();
  });

  // `??` stops at the first NON-NULLISH value and 0 is non-nullish, so a
  // ??-chained source check let keep-last:0 both pick the wrong source and
  // mask every later key.
  it("treats a zero keep-* as 'not set' rather than as retention", () => {
    const s = summarizePrune(
      undefined,
      { "keep-last": 0, "keep-daily": 14 },
      [job({ "keep-daily": 99 })],
      settled,
    );
    expect(s.retention?.["keep-daily"]).toBe(14); // the datastore's, not the job's
  });

  it("calls an unscheduled job manual rather than inventing a schedule", () => {
    const unscheduled: PBSPruneJob = { id: "j1", store: STORE };
    const s = summarizePrune(undefined, {}, [unscheduled], settled);
    expect(s.scheduleLabel).toBe("manual (prune job)");
  });

  it("surfaces a failed job even when a newer job succeeded", () => {
    const s = summarizePrune(
      undefined,
      {},
      [
        job({ id: "old", "last-run-endtime": 100, "last-run-state": "error: disk full" }),
        job({ id: "new", "last-run-endtime": 200, "last-run-state": "OK" }),
      ],
      settled,
    );
    expect(s.lastRun?.at).toBe(200);
    expect(s.failedJob?.id).toBe("old");
  });

  it("reports the soonest next run and no run at all for a fresh job", () => {
    expect(summarizePrune(undefined, {}, [job({ "next-run": 500 }), job({ "next-run": 300 })], settled).nextRun).toBe(300);
    expect(summarizePrune(undefined, {}, [job()], settled).lastRun).toBeUndefined();
  });
});
