import { PALETTE_BLUE } from "@/lib/chart-colors";
import type { PBSDatastoreMetric } from "../types/backup";

/**
 * Series shaping for the "Datastore Usage" chart.
 *
 * Split out from the component for the same reason provider-accents.ts is:
 * a .tsx file that exports both components and plain values breaks React Fast
 * Refresh. Keeping the pivot here also makes it directly testable, which
 * matters because the bug it fixes is invisible on a single-datastore server.
 */

/**
 * Series colours for a chart of several datastores. A fixed palette: the
 * hues only have to be told apart from one another, and an accent the user
 * picked would land on one of them. (A server with one datastore draws it in
 * the data colour instead: see DatastoreChart.) None is green, amber or red:
 * those are states, and a store drawn in one would read as running, warning
 * or failed.
 */
const FIRST_SERIES_COLOR = PALETTE_BLUE;

const SERIES_COLORS: readonly string[] = [
  FIRST_SERIES_COLOR,
  "hsl(var(--muted-foreground))",
  "hsl(190 70% 45%)",
  "hsl(265 60% 65%)",
  "hsl(325 60% 60%)",
];

/**
 * Dash patterns for stores past the first palette cycle.
 *
 * Colour alone stops telling series apart at the sixth datastore — it would
 * reuse the first colour for the stroke, the fill AND the legend swatch, so the
 * legend would become actively misleading exactly when there are enough series
 * to need it. Pairing the cycle with a dash keeps 5 × 3 = 15 stores distinct,
 * after which they genuinely repeat.
 */
const DASH_PATTERNS = ["6 3", "2 3"];

/** Cycles the palette, so any number of datastores gets a colour. */
export function datastoreSeriesColor(index: number): string {
  // The modulo already keeps this in range; the fallback only satisfies
  // noUncheckedIndexedAccess, since the palette is never empty.
  return SERIES_COLORS[index % SERIES_COLORS.length] ?? FIRST_SERIES_COLOR;
}

/**
 * Stroke pattern for a series: solid on the first palette cycle, then dashed,
 * so a repeated colour is still distinguishable.
 *
 * Solid is "0" rather than undefined because the repo runs
 * exactOptionalPropertyTypes, and Recharts types strokeDasharray as
 * `string | number` — an undefined would not type-check. SVG treats a zero-
 * length dash array as a solid stroke.
 */
export function datastoreSeriesDash(index: number): string {
  const cycle = Math.floor(index / SERIES_COLORS.length);
  if (cycle === 0) return "0";
  return DASH_PATTERNS[(cycle - 1) % DASH_PATTERNS.length] ?? "0";
}

/**
 * Datastore names become object keys on the chart rows, so they are namespaced
 * away from the row's own "timestamp" key. A fixed-length prefix is injective,
 * so no two datastores can collide and no datastore — whatever PBS permits as
 * a name — can produce the key "timestamp" and overwrite the x value. (It also
 * neutralises a store called "__proto__", which unprefixed would silently
 * vanish, since assigning a number to it on a plain object is a no-op.) The
 * prefix never reaches the UI: each Area carries the bare name for its legend
 * entry and tooltip label.
 *
 * Names containing a dot are safe, though the reason is not obvious. Recharts
 * resolves a string dataKey through es-toolkit's lodash-compatible `get`
 * (getValueByDataKey in recharts/util/ChartUtils), which treats "." as a path
 * separator — but only after failing to find the literal key. So "ds:a.b"
 * finds the literal property, and when that store is missing from a bucket the
 * traversal it falls back to yields undefined, which is the gap we want.
 * DatastoreChart.test.tsx pins both halves, because a change in that
 * precedence would silently blank a series rather than error.
 */
const SERIES_KEY_PREFIX = "ds:";

export function datastoreSeriesKey(datastore: string): string {
  return SERIES_KEY_PREFIX + datastore;
}

/**
 * One row of the chart: a bucket timestamp plus one key per datastore.
 *
 * The index signature is as tight as runtime-computed keys allow — it pins the
 * value type and keeps `timestamp` non-optional, but it cannot reject an
 * unknown key, so a typo reads as `number | undefined` rather than an error.
 */
export interface DatastoreUsageRow {
  timestamp: number;
  [seriesKey: string]: number | undefined;
}

/**
 * Pivots the API's long-format rows into one chart row per timestamp.
 *
 * The endpoint returns a row per datastore per bucket
 * (GetPBSDatastoreMetricsHistory groups by both), and the collector writes one
 * shared timestamp for every datastore in a cycle, so several rows land on
 * exactly the same x. Feeding those straight to a single Area made it jump
 * between each store's value at every timestamp — neither an aggregate nor a
 * per-store reading, just noise. Pivoting gives each datastore its own series.
 *
 * A datastore missing from a bucket simply has no key in that row, which
 * Recharts draws as a gap rather than interpolating across it. That is the
 * honest rendering: a store added midway through the window has no earlier
 * usage to show.
 */
export function pivotByDatastore(metrics: PBSDatastoreMetric[]): {
  rows: DatastoreUsageRow[];
  datastores: string[];
} {
  const names = new Set<string>();
  const byTimestamp = new Map<number, DatastoreUsageRow>();

  for (const m of metrics) {
    const timestamp = new Date(m.time).getTime();
    // A bad timestamp has no place on a time axis: NaN poisons the domain, and
    // an epoch-zero point stretches it back to 1970 and flattens every real
    // sample against the left edge. isFinite alone is not enough — `time: null`
    // would violate the declared type but yields 0, not NaN.
    if (!Number.isFinite(timestamp) || timestamp <= 0) continue;

    names.add(m.datastore);
    let row = byTimestamp.get(timestamp);
    if (row === undefined) {
      row = { timestamp };
      byTimestamp.set(timestamp, row);
    }
    row[datastoreSeriesKey(m.datastore)] = m.used;
  }

  return {
    // Sorted because a Map preserves insertion order, and a time axis needs
    // its data ascending in x.
    rows: [...byTimestamp.values()].sort((a, b) => a.timestamp - b.timestamp),
    // Sorted so colours and legend order stay stable between refetches
    // regardless of the order the API happened to return rows in.
    datastores: [...names].sort(),
  };
}
