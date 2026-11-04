import type { UpsDevice, UpsEvent, UpsEventKind, UpsMetricPoint } from "../types/ups";

export type UpsTone = "ok" | "warn" | "err" | "idle";

export interface UpsLabel {
  label: string;
  tone: UpsTone;
}

type StatusInput = Pick<
  UpsDevice,
  "state" | "reachability" | "reading_stale" | "server_enabled" | "flags"
>;

/**
 * The status chip for a UPS. Reachability is checked first: a UPS Nexara
 * could not read shows that, never its last known state, because a stale
 * "Online" in an outage is the one wrong answer that matters. The state
 * itself is the server's, derived the way upsmon reads ups.status.
 */
export function upsStatus(d: StatusInput): UpsLabel {
  if (!d.server_enabled) return { label: "Polling off", tone: "idle" };
  if (d.reachability === "unknown") return { label: "Waiting", tone: "idle" };
  if (d.reachability === "unreachable") return { label: "Unreachable", tone: "idle" };
  // Before the reading's age: a driver that is down leaves the last reading
  // where it was, so it goes stale while upsd still answers.
  if (d.reachability === "driver_down") return { label: "Driver down", tone: "idle" };
  if (d.reachability === "stale") return { label: "Data stale", tone: "idle" };
  // Read fine last time, but nothing since: the poller stopped reading.
  if (d.reading_stale) return { label: "No recent reading", tone: "idle" };
  switch (d.state) {
    case "low_battery":
      return {
        label: d.flags.includes("FSD") ? "Shutting down" : "Low battery",
        tone: "err",
      };
    case "on_battery":
      return { label: "On battery", tone: "warn" };
    case "online":
      return { label: "Online", tone: "ok" };
    case "off":
      return { label: "Off", tone: "idle" };
    default:
      return { label: "Unknown", tone: "idle" };
  }
}

/**
 * On battery by a reading Nexara can use: the OB flag, never the low_battery
 * state alone, which FSD on mains power (a shutdown test, or power back
 * mid-shutdown) also lands in. A UPS it cannot read is not on battery here.
 */
export function upsOnBattery(d: Pick<UpsDevice, "state" | "flags">): boolean {
  return d.state !== "unknown" && d.flags.includes("OB");
}

/**
 * Spans of buckets in which the UPS was on battery, as [from, to] timestamps.
 * Each span runs to the END of its last bucket, so an outage inside a single
 * bucket still has a width to shade.
 */
export function onBatteryRuns(points: UpsMetricPoint[], bucketMs: number): [number, number][] {
  const runs: [number, number][] = [];
  let start: number | null = null;
  let last = 0;
  for (const p of points) {
    if (p.on_battery === true) {
      start ??= p.timestamp;
      last = p.timestamp;
    } else if (start !== null) {
      runs.push([start, last + bucketMs]);
      start = null;
    }
  }
  if (start !== null) runs.push([start, last + bucketMs]);
  return runs;
}

const FLAG_LABELS: Record<string, UpsLabel> = {
  RB: { label: "Replace battery", tone: "warn" },
  OVER: { label: "Overload", tone: "err" },
  BYPASS: { label: "Bypass", tone: "warn" },
  ALARM: { label: "Alarm", tone: "warn" },
  CAL: { label: "Calibrating", tone: "idle" },
  TRIM: { label: "Trimming voltage", tone: "idle" },
  BOOST: { label: "Boosting voltage", tone: "idle" },
  CHRG: { label: "Charging", tone: "idle" },
  DISCHRG: { label: "Discharging", tone: "idle" },
  HB: { label: "High battery", tone: "idle" },
  LB: { label: "Low battery", tone: "warn" },
};

/**
 * Chips for the flags the status chip does not already say. OL, OB, FSD and
 * OFF are the state; LB is too when the state is low_battery. An unknown flag
 * is shown as upsd sent it rather than dropped.
 */
export function upsFlagChips(d: Pick<UpsDevice, "state" | "flags">): UpsLabel[] {
  const covered = new Set(["OL", "OB", "FSD", "OFF"]);
  if (d.state === "low_battery") covered.add("LB");
  return d.flags
    .filter((f) => !covered.has(f))
    .map((f) => FLAG_LABELS[f] ?? { label: f, tone: "idle" });
}

/** Seconds as "24 min", "9m 40s" or "1h 05m". */
export function formatRuntime(seconds: number | null | undefined): string {
  if (seconds == null || !Number.isFinite(seconds)) return "—";
  const s = Math.max(0, Math.round(seconds));
  if (s >= 3600) {
    return `${String(Math.floor(s / 3600))}h ${String(Math.floor((s % 3600) / 60)).padStart(2, "0")}m`;
  }
  if (s >= 600) return `${String(Math.round(s / 60))} min`;
  if (s >= 60) return `${String(Math.floor(s / 60))}m ${String(s % 60).padStart(2, "0")}s`;
  return `${String(s)}s`;
}

/** Watts, marked "≈" when the server estimated them from load x nominal. */
export function formatWatts(w: number | null, estimated: boolean): string {
  if (w == null) return "—";
  return `${estimated ? "≈" : ""}${String(Math.round(w))} W`;
}

const EVENT_LABELS: Record<UpsEventKind, string> = {
  on_battery: "Power outage",
  low_battery: "Low battery",
  overload: "Overload",
  bypass: "On bypass",
  voltage_correction: "Voltage correction",
  replace_battery: "Battery needs replacing",
  unreachable: "Lost contact",
  stale: "No fresh data from the driver",
  self_test: "Self-test",
};

/** Whether a string is one of the power-event kinds, as a History entry's
 *  action carries it. Not Object.hasOwn, which is newer than the browsers the
 *  build targets. */
export function isUpsEventKind(kind: string): kind is UpsEventKind {
  return Object.prototype.hasOwnProperty.call(EVENT_LABELS, kind);
}

export function eventLabel(e: Pick<UpsEvent, "kind" | "details">): string {
  if (e.kind === "self_test" && e.details.result) {
    return `Self-test: ${e.details.result}`;
  }
  // FSD opens a low-battery episode whatever the line power, as upsmon reads it.
  if (e.kind === "low_battery" && /\bFSD\b/.test(e.details.status ?? "")) {
    return "Forced shutdown";
  }
  return EVENT_LABELS[e.kind];
}

export function eventTone(e: Pick<UpsEvent, "kind" | "details">): UpsTone {
  switch (e.kind) {
    case "low_battery":
    case "overload":
      return "err";
    case "on_battery":
    case "bypass":
    case "replace_battery":
      return "warn";
    case "self_test":
      return /passed/i.test(e.details.result ?? "") ? "ok" : "warn";
    default:
      return "idle";
  }
}

/**
 * How long an event lasted, "ongoing · 6m 40s" while it is open, and "—" for
 * a point event. An end Nexara did not see because contact was lost is marked
 * "≤": the episode ended at some point before it was closed. One closed
 * because Nexara stopped watching (polling turned off, the UPS no longer
 * listed) is marked "≥": it was still going then.
 */
export function eventDuration(
  e: Pick<UpsEvent, "started_at" | "ended_at" | "end_observed" | "kind"> & {
    details?: Pick<UpsEvent["details"], "ended_by">;
  },
  now: number,
): string {
  if (e.kind === "self_test") return "—";
  const start = Date.parse(e.started_at);
  if (e.ended_at === null) return `ongoing · ${formatRuntime((now - start) / 1000)}`;
  const span = formatRuntime((Date.parse(e.ended_at) - start) / 1000);
  if (e.details?.ended_by !== undefined) return `≥ ${span}`;
  return e.end_observed ? span : `≤ ${span}`;
}

/** The Ended field: when, and if Nexara did not see it, why not. */
export function eventEnded(e: Pick<UpsEvent, "ended_at" | "end_observed" | "details">, at: string): string {
  if (e.ended_at === null) return "Ongoing";
  switch (e.details.ended_by) {
    case "polling_off":
      return `Not seen: polling was turned off at ${at}`;
    case "removed":
      return `Not seen: upsd stopped listing the UPS at ${at}`;
  }
  return e.end_observed ? at : `By ${at} (not seen: Nexara had lost contact)`;
}
