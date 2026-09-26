import type { ResourceKind } from "../types/vm";

/** Proxmox's cap on a snapshot name — proxmox.SnapshotMaxNameLen. */
const SNAPSHOT_NAME_MAX_LENGTH = 40;

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
 * The authority is proxmox.ReservedSnapshotName in
 * internal/proxmox/client_guests.go; the two must not drift. (It lived in
 * internal/api/handlers/vms.go until the rule moved to the client choke point,
 * so that the scheduler and the guest-tools engine share it too.)
 */
export const SNAPSHOT_NAME_RULES = `Must start with a letter and use only letters, numbers, '-' and '_' — no spaces (2–${String(SNAPSHOT_NAME_MAX_LENGTH)} characters).`;

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
  if (name.length > SNAPSHOT_NAME_MAX_LENGTH)
    return `Snapshot names are limited to ${String(SNAPSHOT_NAME_MAX_LENGTH)} characters.`;
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

/*
 * --- Scheduled snapshots ---
 *
 * A snapshot schedule stores a PREFIX, not a name. Every run names its
 * snapshot `<prefix>-YYYYMMDD-HHMMSS` from its date and time in UTC, "auto"
 * standing in when the prefix is empty — because a guest holds each snapshot
 * name once, and a name sent verbatim on every run failed on every run after
 * the first. In UTC rather than on the server's clock or the browser's: a
 * local clock with daylight saving shows one hour twice each autumn, the cron
 * runs on the server's clock and fires the repeated time again, and a name
 * read off that clock could repeat. The authority is
 * proxmox.TimestampedSnapshotName and
 * proxmox.ValidateSnapshotNamePrefix in internal/proxmox/client_guests.go,
 * with the prefix default in internal/scheduler (scheduledSnapshotName); the
 * constants below mirror them and must not drift.
 */

/**
 * What a run adds to the prefix, as a pattern: a dash, then the run's date and
 * time in UTC. The scheduler writes digits (Go layout "20060102-150405"); this
 * is the shape, for display and for the length budget.
 */
export const SCHEDULED_SNAPSHOT_SUFFIX = "-YYYYMMDD-HHMMSS";

/**
 * The longest prefix a schedule can store: what the suffix leaves of the 40,
 * which is 24 — proxmox.SnapshotNamePrefixMaxLen, which the API enforces on
 * create and update.
 */
export const SNAPSHOT_NAME_PREFIX_MAX_LENGTH =
  SNAPSHOT_NAME_MAX_LENGTH - SCHEDULED_SNAPSHOT_SUFFIX.length;

/** The prefix a run uses when the schedule stores none. */
export const AUTO_SNAPSHOT_PREFIX = "auto";

export const SNAPSHOT_PREFIX_RULES = `Must start with a letter and use only letters, numbers, '-' and '_' — no spaces (up to ${String(SNAPSHOT_NAME_PREFIX_MAX_LENGTH)} characters).`;

/** The name a schedule's runs take, as a pattern: "nightly-YYYYMMDD-HHMMSS". */
export function scheduledSnapshotNamePattern(prefix: string): string {
  return `${prefix || AUTO_SNAPSHOT_PREFIX}${SCHEDULED_SNAPSHOT_SUFFIX}`;
}

/**
 * Validates a snapshot schedule's prefix. What Proxmox judges is the name a
 * run sends — the prefix, then the run's date and time — so that is what is
 * checked, by the whole-name rule above; the API does the same. Checking the
 * prefix as a name would get two answers wrong: a reserved word is a legal
 * prefix ("current-20260926-020000" is not reserved), and so is one letter.
 */
export function snapshotPrefixError(
  prefix: string,
  kind: ResourceKind,
): string | null {
  if (prefix.length === 0) return null;
  // First, and in the prefix's own terms: past the budget the whole-name rule
  // would answer "limited to 40 characters", which is not the limit the field
  // is typed against.
  if (prefix.length > SNAPSHOT_NAME_PREFIX_MAX_LENGTH)
    return `A prefix is limited to ${String(SNAPSHOT_NAME_PREFIX_MAX_LENGTH)} characters, which leaves room for the date and time each run adds.`;
  // Any date gives the same verdict — the suffix is fixed-width digits — so
  // zeros stand in for the run's own.
  return snapshotNameError(
    prefix + SCHEDULED_SNAPSHOT_SUFFIX.replace(/[A-Z]/g, "0"),
    kind,
  );
}
