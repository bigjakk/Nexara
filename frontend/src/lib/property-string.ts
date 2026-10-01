/**
 * Proxmox property strings (`key=value,key=value`, optionally with a bare
 * default_key segment), edited in place.
 *
 * Use these when a value must be written back with every segment this code
 * has no field for keeping its bytes and its position, or when the format
 * has a default_key (a NIC's model, a VGA type, a node's Wake-on-LAN MAC):
 * a segment is order- and byte-preserving, and a segment with no "=" is
 * modelled as the default key's value. Rebuilding an untouched value then
 * yields exactly what was stored, so comparing the two is the dirty check.
 *
 * features/clusters/lib/prop-string.ts is the other tool. It parses into a
 * map and serializes from the map, which suits a value of named keys that is
 * replaced whole, but a bare default_key segment has no value there and is
 * lost, and the rest is normalised. Do not rewrite a value with it where
 * either matters.
 */

/**
 * One comma-separated segment of a Proxmox property string. `key` is null
 * for a segment with no "=", which holds the value of the format's
 * default_key (a NIC's model, a VGA type). `raw` is the segment as stored.
 */
export interface Segment {
  key: string | null;
  value: string;
  raw: string;
}

/**
 * Split one segment as pve-common's parse_property_string
 * (src/PVE/JSONSchema.pm) does: at its first "=", or not at all when it has
 * none. Key and value are trimmed for reading; `raw` is left as it was.
 */
export function segment(raw: string): Segment {
  const idx = raw.indexOf("=");
  if (idx === -1) return { key: null, value: raw.trim(), raw };
  return {
    key: raw.slice(0, idx).trim(),
    value: raw.slice(idx + 1).trim(),
    raw,
  };
}

export function splitSegments(raw: string): Segment[] {
  return raw === "" ? [] : raw.split(",").map(segment);
}

export function joinSegments(segs: Segment[]): string {
  return segs.map((s) => s.raw).join(",");
}

/**
 * Write `text` in place of the segments `owns` matches: where the first one
 * stands, dropping any later match. When nothing matches it is appended, or
 * prepended for a default_key. `text` null removes the matches. Every other
 * segment keeps its bytes and its position.
 */
export function setSegment(
  segs: Segment[],
  owns: (s: Segment) => boolean,
  text: string | null,
  whenAbsent: "append" | "prepend" = "append",
): Segment[] {
  const out: Segment[] = [];
  let found = false;
  for (const s of segs) {
    if (!owns(s)) {
      out.push(s);
      continue;
    }
    if (!found && text !== null) out.push(segment(text));
    found = true;
  }
  if (found || text === null) return out;
  return whenAbsent === "prepend"
    ? [segment(text), ...out]
    : [...out, segment(text)];
}
