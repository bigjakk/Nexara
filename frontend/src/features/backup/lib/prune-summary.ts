import type { PBSPruneJob } from "../types/backup";

/** The datastore's own config, or a prune job — both carry the keep-* keys. */
type RetentionSource = Partial<
  Record<
    | "keep-last"
    | "keep-hourly"
    | "keep-daily"
    | "keep-weekly"
    | "keep-monthly"
    | "keep-yearly",
    number
  >
>;

export const RETENTION_KEYS = [
  ["keep-last", "Keep Last"],
  ["keep-hourly", "Keep Hourly"],
  ["keep-daily", "Keep Daily"],
  ["keep-weekly", "Keep Weekly"],
  ["keep-monthly", "Keep Monthly"],
  ["keep-yearly", "Keep Yearly"],
] as const;

/** True when a source sets any retention at all. */
function hasRetention(src: RetentionSource): boolean {
  // (x ?? 0) > 0 rather than a ??-chain: `??` stops at the first NON-NULLISH
  // value and 0 is non-nullish, so `keep-last: 0` would both select the wrong
  // source and mask every later key.
  return RETENTION_KEYS.some(([k]) => (src[k] ?? 0) > 0);
}

export interface PruneSummary {
  /** What to print for "Prune Schedule". Never claims more than is known. */
  scheduleLabel: string;
  /** Retention to display, or null when there is no single honest answer. */
  retention: RetentionSource | null;
  /** Shown instead of retention values when jobs disagree. */
  retentionNote?: string;
  lastRun?: { at: number; state?: string | undefined };
  /** Any active job whose last run did not end OK — not just the newest. */
  failedJob?: { id: string; state: string };
  nextRun?: number;
}

/**
 * Reduces a datastore's own config plus its prune jobs to what the card can
 * honestly say.
 *
 * The point of the whole exercise is not to guess: PBS 2.2 moved prune out of
 * datastore.cfg into separate jobs, so reading only the datastore reported
 * "Not set" on datastores pruning daily. Over-claiming in the other direction
 * is the same bug though — a namespace-scoped job does not prune the whole
 * datastore, a disabled job does not prune at all, and a request that failed
 * establishes nothing. Each of those gets its own label.
 */
export function summarizePrune(
  ownSchedule: string | undefined,
  ownRetention: RetentionSource,
  jobs: PBSPruneJob[] | undefined,
  state: { loading: boolean; failed: boolean },
): PruneSummary {
  const active = (jobs ?? []).filter((j) => !j.disable);

  const retention: RetentionSource | null = hasRetention(ownRetention)
    ? ownRetention
    : null;

  const base: PruneSummary = { scheduleLabel: "", retention };

  // The datastore's own schedule still wins where it exists (older PBS, or a
  // store carrying legacy keys) — it describes the datastore itself.
  if (ownSchedule) {
    return { ...base, scheduleLabel: ownSchedule };
  }
  if (state.loading) {
    return { ...base, scheduleLabel: "Loading…" };
  }
  if (state.failed) {
    // Distinct from "Not configured": we asked and did not find out.
    return { ...base, scheduleLabel: "Unknown — prune jobs unavailable" };
  }
  if (active.length === 0) {
    return {
      ...base,
      scheduleLabel: (jobs ?? []).length > 0 ? "Disabled" : "Not configured",
    };
  }

  // A job with no schedule exists but only runs when someone runs it.
  const schedules = [...new Set(active.map((j) => j.schedule ?? "manual"))];
  const parts = [schedules.join(", ")];
  parts.push(active.length > 1 ? `${String(active.length)} jobs` : "prune job");
  // A namespace-scoped job prunes part of the datastore, not the datastore.
  const namespaces = [...new Set(active.map((j) => j.ns).filter((n) => !!n))];
  if (namespaces.length > 0) {
    parts.push(
      namespaces.length === active.length
        ? `namespace ${namespaces.join(", ")}`
        : `some namespace-scoped`,
    );
  }
  const scheduleLabel = `${parts[0] ?? ""} (${parts.slice(1).join(", ")})`;

  // Retention: the datastore's own if set, else the jobs' — but only when they
  // agree, since picking an arbitrary job would present one job's policy as
  // the datastore's.
  let jobRetention = retention;
  let retentionNote: string | undefined;
  if (!jobRetention) {
    const signatures = new Set(
      active.map((j) => RETENTION_KEYS.map(([k]) => j[k] ?? 0).join("/")),
    );
    if (signatures.size === 1) {
      jobRetention = active[0] ?? null;
    } else {
      retentionNote = `varies across ${String(active.length)} jobs`;
    }
  }

  const runs = active
    .filter((j) => j["last-run-endtime"] != null)
    .map((j) => ({ at: j["last-run-endtime"] ?? 0, state: j["last-run-state"] }))
    .sort((a, b) => b.at - a.at);
  const failed = active.find(
    (j) => !!j["last-run-state"] && j["last-run-state"] !== "OK",
  );
  const nextRuns = active
    .map((j) => j["next-run"])
    .filter((t): t is number => t != null)
    .sort((a, b) => a - b);

  return {
    scheduleLabel,
    retention: jobRetention,
    ...(retentionNote != null ? { retentionNote } : {}),
    ...(runs[0] != null ? { lastRun: runs[0] } : {}),
    ...(failed != null
      ? { failedJob: { id: failed.id, state: failed["last-run-state"] ?? "" } }
      : {}),
    ...(nextRuns[0] != null ? { nextRun: nextRuns[0] } : {}),
  };
}
