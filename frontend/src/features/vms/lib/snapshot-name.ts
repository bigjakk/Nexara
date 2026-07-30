/**
 * Proxmox snapshot names follow the pve-configid format: a leading letter,
 * then letters, digits, '-' or '_', capped at 40 characters. "current" is
 * reserved for the live state. Validating here surfaces the rules before
 * Proxmox rejects the name with a cryptic "invalid configid" error.
 */
export const SNAPSHOT_NAME_RULES =
  "Must start with a letter and use only letters, numbers, '-' and '_' — no spaces (2–40 characters).";

export function snapshotNameError(name: string): string | null {
  if (name.length === 0) return null;
  if (/\s/.test(name)) return "Snapshot names cannot contain spaces.";
  if (!/^[A-Za-z]/.test(name)) return "Snapshot names must start with a letter.";
  if (/[^A-Za-z0-9_-]/.test(name))
    return "Only letters, numbers, '-' and '_' are allowed.";
  if (name.length < 2) return "Snapshot names need at least 2 characters.";
  if (name.length > 40) return "Snapshot names are limited to 40 characters.";
  if (name === "current") return '"current" is reserved by Proxmox.';
  return null;
}
