import { describe, it, expect } from "vitest";
import {
  pivotByDatastore,
  datastoreSeriesKey,
  datastoreSeriesColor,
  datastoreSeriesDash,
} from "./datastore-usage-series";
import type { PBSDatastoreMetric } from "../types/backup";

function metric(
  time: string,
  datastore: string,
  used: number,
): PBSDatastoreMetric {
  return {
    time,
    pbs_server_id: "pbs-1",
    datastore,
    total: 1_000,
    used,
    avail: 1_000 - used,
  };
}

const T1 = "2026-09-06T10:00:00Z";
const T2 = "2026-09-06T11:00:00Z";

describe("pivotByDatastore", () => {
  it("gives each datastore its own series on a shared timestamp", () => {
    // The bug: the collector writes one shared timestamp per cycle, so rows
    // for different datastores land on exactly the same x. Flattened into a
    // single series they made the line jump between stores at every point.
    const { rows, datastores } = pivotByDatastore([
      metric(T1, "store-a", 10),
      metric(T1, "store-b", 20),
      metric(T2, "store-a", 11),
      metric(T2, "store-b", 21),
    ]);

    expect(datastores).toEqual(["store-a", "store-b"]);
    expect(rows).toHaveLength(2);
    expect(rows[0]).toMatchObject({
      timestamp: Date.parse(T1),
      [datastoreSeriesKey("store-a")]: 10,
      [datastoreSeriesKey("store-b")]: 20,
    });
    expect(rows[1]).toMatchObject({
      timestamp: Date.parse(T2),
      [datastoreSeriesKey("store-a")]: 11,
      [datastoreSeriesKey("store-b")]: 21,
    });
  });

  it("leaves a gap for a datastore absent from a bucket", () => {
    // A store added midway through the window has no earlier usage. Recharts
    // draws the missing key as a gap, which is the honest rendering — filling
    // it would invent history.
    const { rows } = pivotByDatastore([
      metric(T1, "store-a", 10),
      metric(T2, "store-a", 11),
      metric(T2, "store-b", 21),
    ]);

    expect(rows[0]).not.toHaveProperty(datastoreSeriesKey("store-b"));
    expect(rows[1]).toHaveProperty(datastoreSeriesKey("store-b"), 21);
  });

  it("sorts rows ascending in time whatever order the API returned", () => {
    // A number/time axis requires ascending x; Map preserves insertion order.
    const { rows } = pivotByDatastore([
      metric(T2, "store-a", 11),
      metric(T1, "store-a", 10),
    ]);

    expect(rows.map((r) => r.timestamp)).toEqual([
      Date.parse(T1),
      Date.parse(T2),
    ]);
  });

  it("orders datastores stably so colours do not shuffle between refetches", () => {
    const first = pivotByDatastore([
      metric(T1, "zeta", 1),
      metric(T1, "alpha", 2),
    ]).datastores;
    const second = pivotByDatastore([
      metric(T1, "alpha", 2),
      metric(T1, "zeta", 1),
    ]).datastores;

    expect(first).toEqual(second);
  });

  it('does not let a datastore named "timestamp" clobber the x value', () => {
    // "timestamp" is a legal PBS datastore name. Without namespacing the key,
    // its usage figure would overwrite the bucket time and the point would
    // land in 1970.
    const { rows } = pivotByDatastore([metric(T1, "timestamp", 42)]);

    expect(rows[0]?.timestamp).toBe(Date.parse(T1));
    expect(rows[0]?.[datastoreSeriesKey("timestamp")]).toBe(42);
  });

  it("drops samples whose time cannot be parsed", () => {
    // One NaN would poison the whole axis domain.
    const { rows, datastores } = pivotByDatastore([
      metric("not a date", "store-a", 10),
      metric(T1, "store-a", 11),
    ]);

    expect(rows).toHaveLength(1);
    expect(rows[0]?.timestamp).toBe(Date.parse(T1));
    expect(datastores).toEqual(["store-a"]);
  });

  it("keeps a datastore whose name contains a dot on its own key", () => {
    // Dots are legal in PBS datastore names and are a path separator to the
    // resolver Recharts uses, so this pairs with the render test that proves
    // the series still draws.
    const { rows, datastores } = pivotByDatastore([
      metric(T1, "backup.daily", 7),
      metric(T1, "store-a", 9),
    ]);

    expect(datastores).toEqual(["backup.daily", "store-a"]);
    expect(rows[0]?.[datastoreSeriesKey("backup.daily")]).toBe(7);
  });

  it("returns nothing for no samples", () => {
    expect(pivotByDatastore([])).toEqual({ rows: [], datastores: [] });
  });
});

describe("datastoreSeriesColor", () => {
  it("gives distinct colours to the first several datastores", () => {
    const colors = [0, 1, 2, 3, 4].map(datastoreSeriesColor);
    expect(new Set(colors).size).toBe(colors.length);
  });

  it("cycles rather than running out", () => {
    expect(datastoreSeriesColor(5)).toBe(datastoreSeriesColor(0));
    expect(datastoreSeriesColor(11)).toBe(datastoreSeriesColor(1));
  });
});

describe("datastoreSeriesDash", () => {
  it("draws the first palette cycle solid", () => {
    for (const i of [0, 1, 2, 3, 4]) {
      expect(datastoreSeriesDash(i)).toBe("0");
    }
  });

  it("dashes a repeated colour so the legend still distinguishes it", () => {
    // Index 5 reuses index 0's colour; without a different stroke the legend
    // swatch would be identical and stop telling the two stores apart.
    expect(datastoreSeriesColor(5)).toBe(datastoreSeriesColor(0));
    expect(datastoreSeriesDash(5)).not.toBe(datastoreSeriesDash(0));
  });

  it("keeps colour+dash pairs distinct across the whole cycle", () => {
    const pairs = Array.from(
      { length: 15 },
      (_, i) => `${datastoreSeriesColor(i)}|${datastoreSeriesDash(i)}`,
    );
    expect(new Set(pairs).size).toBe(pairs.length);
  });
});
