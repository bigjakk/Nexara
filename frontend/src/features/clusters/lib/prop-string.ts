/**
 * Proxmox "property strings" — `key=value,key=value` — appear all over the
 * cluster options API (`bwlimit`, `next-id`, `ha`, `crs`, `migration`, …).
 * Helpers to parse, serialize, and compare them.
 *
 * On the wire Proxmox 7+ may return either the raw string or an
 * already-parsed object; callers should pass the value through `toStr()`
 * first if they're unsure.
 */

export type PropString = Record<string, string>;

/** Parse `"key=value,key=value"` into a map. Whitespace tolerant. Empty
 *  input → empty map. Pairs without `=` are kept as `key="" "`. */
export function parsePropString(input: string | undefined | null): PropString {
  const out: PropString = {};
  if (!input) return out;
  for (const part of input.split(",")) {
    const trimmed = part.trim();
    if (!trimmed) continue;
    const eq = trimmed.indexOf("=");
    if (eq < 0) {
      out[trimmed] = "";
    } else {
      const k = trimmed.slice(0, eq).trim();
      const v = trimmed.slice(eq + 1).trim();
      if (k) out[k] = v;
    }
  }
  return out;
}

/** Serialize a map back to `"key=value,key=value"`. Skips entries whose
 *  value is empty. Stable key order = insertion order. */
export function serializePropString(obj: PropString): string {
  return Object.entries(obj)
    .filter(([, v]) => v !== "")
    .map(([k, v]) => `${k}=${v}`)
    .join(",");
}

/** True if two property-string values represent the same key-value set,
 *  regardless of ordering or whitespace. */
export function propStringsEqual(a: string | undefined | null, b: string | undefined | null): boolean {
  const pa = parsePropString(a);
  const pb = parsePropString(b);
  const ka = Object.keys(pa).sort();
  const kb = Object.keys(pb).sort();
  if (ka.length !== kb.length) return false;
  for (let i = 0; i < ka.length; i += 1) {
    const key = ka[i];
    if (key !== kb[i]) return false;
    if (key === undefined) return false;
    if (pa[key] !== pb[key]) return false;
  }
  return true;
}
