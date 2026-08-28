/**
 * Veeam's duration text, which it passes through from .NET's TimeSpan.
 *
 * Days are prefixed with a DOT, not a colon: "1.02:15:00" is 26h15m. Splitting
 * on ":" still yields three parts there, and `Number("1.02")` is 1.02 rather
 * than NaN, so a naive parse reads the longest run in the table as 1h16m and
 * files it below a two-hour one.
 *
 * Anchored and digits-only, so anything unrecognised returns null and sorts as
 * unknown rather than as a confident wrong number. `Number("")` being 0 is the
 * same trap from the other end: "0:0:" would otherwise sort as the shortest
 * run in the table.
 */
const DURATION_PATTERN = /^(?:(\d+)\.)?(\d+):([0-5]\d):([0-5]\d)$/;

export function veeamDurationSeconds(value: string): number | null {
  const match = DURATION_PATTERN.exec(value);
  if (match === null) return null;
  return (
    Number(match[1] ?? "0") * 86400 +
    Number(match[2] ?? "0") * 3600 +
    Number(match[3] ?? "0") * 60 +
    Number(match[4] ?? "0")
  );
}
