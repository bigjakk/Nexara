import { joinSegments, setSegment, splitSegments } from "@/lib/property-string";
import { pveBoolean } from "./vm-config-parsers";

/*
 * pve-container's $features_desc (src/PVE/LXC/Config.pm), transcribed:
 *
 *   mount         "fstype;fstype;...": the file system types the container
 *                 may mount.
 *   nesting, keyctl, fuse, mknod, force_rw_sys
 *                 booleans, each 0 by default (see pveBoolean).
 *
 * There is no default_key. update_pct_config stores the value as it was
 * sent, so the keys come in whatever order, and the booleans in whatever
 * spelling, it was written with. force_rw_sys has no field here, and
 * buildFeatures keeps it.
 */

/** The features the Resources form edits. */
export interface CTFeatures {
  nesting: boolean;
  fuse: boolean;
  keyctl: boolean;
  mknod: boolean;
  mount: string;
}

/** The booleans the form edits, in the order a new value writes them. */
const FEATURE_FLAGS = ["nesting", "fuse", "keyctl", "mknod"] as const;

export function parseFeatures(raw: string): CTFeatures {
  const result: CTFeatures = {
    nesting: false,
    fuse: false,
    keyctl: false,
    mknod: false,
    mount: "",
  };
  for (const seg of splitSegments(raw)) {
    switch (seg.key) {
      case "nesting":
      case "fuse":
      case "keyctl":
      case "mknod":
        result[seg.key] = pveBoolean(seg.value);
        break;
      case "mount":
        result.mount = seg.value;
        break;
    }
  }
  return result;
}

/**
 * Build features on `base`, the value as stored, the way buildNet builds a
 * NIC: only an option whose field differs from what `base` holds is
 * rewritten, where it stands, and every other segment keeps its bytes and
 * its position. So an untouched value rebuilds to exactly `base`, whatever
 * its order, and comparing the two is the dirty check. A flag turned off,
 * or a mount list cleared, is dropped: absent is off.
 */
export function buildFeatures(parsed: CTFeatures, base = ""): string {
  const was = parseFeatures(base);
  let segs = splitSegments(base);
  for (const key of FEATURE_FLAGS) {
    if (parsed[key] === was[key]) continue;
    segs = setSegment(
      segs,
      (s) => s.key === key,
      parsed[key] ? `${key}=1` : null,
    );
  }
  if (parsed.mount !== was.mount) {
    segs = setSegment(
      segs,
      (s) => s.key === "mount",
      parsed.mount ? `mount=${parsed.mount}` : null,
    );
  }
  return joinSegments(segs);
}
