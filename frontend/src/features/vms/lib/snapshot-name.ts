import type { ResourceKind } from "../types/vm";

/**
 * Proxmox snapshot names follow the pve-configid format: a leading letter,
 * then letters, digits, '-' or '_', capped at 40 characters. Validating here
 * surfaces the rules before Proxmox rejects the name with a cryptic "invalid
 * configid" error.
 *
 * The reserved set is asymmetric by guest kind, and upstream matches only one
 * of the three case-insensitively:
 *   - "current" — both kinds, exact. "Current" IS accepted; upstream compares
 *     it with eq, not lc, so rejecting it would refuse a name Proxmox takes.
 *   - "pending" — VMs only, case-insensitive ("Pending", "PENDING" all refused).
 *     Containers deliberately do not reserve it: an LXC config spells that
 *     section "[pve:pending]", and a colon is not a legal configid character,
 *     so there is nothing for a snapshot named "pending" to collide with.
 *   - "vzdump" — containers only, exact. "VZDump" IS accepted.
 *
 * The authority is reservedSnapshotName in internal/api/handlers/vms.go; the
 * two must not drift.
 */
export const SNAPSHOT_NAME_RULES =
  "Must start with a letter and use only letters, numbers, '-' and '_' — no spaces (2–40 characters).";

/**
 * Reserved names per guest kind, split by how upstream compares them. A
 * Record keyed on ResourceKind — rather than a pair of `if`s — so that adding
 * a third kind is a compile error instead of a silent fall-through to some
 * other kind's reserved set.
 */
const RESERVED_NAMES: Record<
  ResourceKind,
  { exact: readonly string[]; caseInsensitive: readonly string[] }
> = {
  vm: { exact: ["current"], caseInsensitive: ["pending"] },
  ct: { exact: ["current", "vzdump"], caseInsensitive: [] },
};

export function snapshotNameError(
  name: string,
  kind: ResourceKind,
): string | null {
  if (name.length === 0) return null;
  if (/\s/.test(name)) return "Snapshot names cannot contain spaces.";
  if (!/^[A-Za-z]/.test(name))
    return "Snapshot names must start with a letter.";
  if (/[^A-Za-z0-9_-]/.test(name))
    return "Only letters, numbers, '-' and '_' are allowed.";
  if (name.length < 2) return "Snapshot names need at least 2 characters.";
  if (name.length > 40) return "Snapshot names are limited to 40 characters.";
  // Reserved names are checked last, after the shape rules, on purpose: the
  // char-class rule above has already rejected every non-ASCII input, so a
  // plain toLowerCase() here is an exact match for Go's strings.EqualFold.
  //
  // No test pins that ordering, and it cannot be pinned today: across all of
  // Unicode only U+212A KELVIN SIGN lowercases to a bare ASCII letter ("k"),
  // and no reserved name contains a "k", so moving this block above the
  // char-class rule changes no answer. It stops being free the moment a
  // case-insensitive reserved name with a "k" in it is added — at which point
  // a test becomes possible, and this comment stops being the only guard.
  const { exact, caseInsensitive } = RESERVED_NAMES[kind];
  if (exact.includes(name) || caseInsensitive.includes(name.toLowerCase()))
    // Quotes what the user typed, not the canonical token, to match the
    // server's own "snap_name %q is reserved by Proxmox".
    return `"${name}" is reserved by Proxmox.`;
  return null;
}
