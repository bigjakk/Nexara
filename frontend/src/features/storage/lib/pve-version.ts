/**
 * isPVEAtLeast returns true if `current` is >= `min` per a lax major.minor[.patch] compare.
 * Empty or unparseable versions return false. Used to feature-gate UI on PVE capabilities
 * (e.g. OCI image pull requires 9.1+).
 */
export function isPVEAtLeast(current: string, min: string): boolean {
  const a = parseVersion(current);
  const b = parseVersion(min);
  if (!a || !b) return false;
  if (a[0] !== b[0]) return a[0] > b[0];
  if (a[1] !== b[1]) return a[1] > b[1];
  return a[2] >= b[2];
}

function parseVersion(v: string): [number, number, number] | null {
  if (!v) return null;
  const match = /^(\d+)(?:\.(\d+))?(?:\.(\d+))?/.exec(v.trim());
  if (!match) return null;
  return [
    Number(match[1] ?? 0),
    Number(match[2] ?? 0),
    Number(match[3] ?? 0),
  ];
}
