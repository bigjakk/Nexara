/**
 * Client-side encryption for a Proxmox Backup Server storage, as the storage
 * dialogs offer it — modelled on the Proxmox GUI's Encryption tab
 * (pve-manager www/manager6/storage/PBSEdit.js, PBSEncryptionKeyTab).
 *
 * storage.cfg never holds the key itself. PBSPlugin writes the key to
 * /etc/pve/priv/storage/<storage>.enc and records only its fingerprint — or
 * the placeholder 1 for a key without one — as `encryption-key`
 * (on_add_hook / on_update_hook, pve-storage src/PVE/Storage/PBSPlugin.pm).
 * That fingerprint is all the config read returns, which is why an existing
 * key is shown as a status line and never loaded into a field.
 */

/**
 * What the dialog does with the key.
 *
 * - keep: leave the storage's key alone (edit, when it has one)
 * - none: no key, and none wanted
 * - autogen: have Proxmox generate one; it comes back once, in the response
 * - existing: install a key the operator pasted or loaded from a file
 * - remove: delete the storage's key
 */
export type PBSEncryptionMode =
  "keep" | "none" | "autogen" | "existing" | "remove";

export interface PBSEncryptionChoice {
  mode: PBSEncryptionMode;
  /** The key file's JSON, for mode "existing". */
  keyText: string;
}

/** The choice a dialog starts from: leave an existing key alone, add none. */
export function initialPBSEncryption(hasKey: boolean): PBSEncryptionChoice {
  return { mode: hasKey ? "keep" : "none", keyText: "" };
}

/**
 * A fingerprint as storage.cfg records it. PBSEdit.js tells one from the
 * placeholder 1 with this same test.
 */
const PBS_FINGERPRINT = /^[a-fA-F0-9]{2}:/;

export interface PBSKeyStatus {
  /** The key's full fingerprint, or null when storage.cfg holds the 1 placeholder. */
  fingerprint: string | null;
  /**
   * The first 8 bytes, the form Proxmox shows everywhere a key is named
   * (render_pbs_fingerprint in pve-manager www/manager6/Utils.js).
   */
  shortFingerprint: string | null;
}

/**
 * Reads a storage's encryption status from the config read's `encryption-key`:
 * null when the storage has no key.
 */
export function pbsKeyStatus(value: string | undefined): PBSKeyStatus | null {
  if (value === undefined || value === "") return null;
  if (!PBS_FINGERPRINT.test(value)) {
    return { fingerprint: null, shortFingerprint: null };
  }
  return { fingerprint: value, shortFingerprint: value.substring(0, 23) };
}

/**
 * The fingerprint a key file names, in the same two forms — or null when the
 * text is no key file, or one without a fingerprint. It is the value PBSPlugin
 * records in storage.cfg for the key (`$decoded_key->{fingerprint}` in
 * on_add_hook and on_update_hook), so it can be matched against the one a
 * storage shows.
 */
export function pbsKeyFileFingerprint(
  keyText: string,
): { fingerprint: string; shortFingerprint: string } | null {
  let parsed: unknown;
  try {
    parsed = JSON.parse(keyText);
  } catch {
    return null;
  }
  if (
    typeof parsed !== "object" ||
    parsed === null ||
    !("fingerprint" in parsed)
  ) {
    return null;
  }
  const value = parsed.fingerprint;
  if (typeof value !== "string") return null;
  const status = pbsKeyStatus(value);
  if (!status?.fingerprint || !status.shortFingerprint) return null;
  return {
    fingerprint: status.fingerprint,
    shortFingerprint: status.shortFingerprint,
  };
}

/**
 * Why a pasted key would be refused, or null when it would not.
 *
 * The rule is PBSPlugin's own: the text must parse as JSON and carry a `data`
 * member (`decode_json` then `exists($decoded_key->{data})` in on_add_hook and
 * on_update_hook) — the same test PBSEdit.js applies before submitting. Only
 * a JSON object can carry a member, so anything else is refused too; and the
 * literal "autogen" is refused here as not being JSON, which matters, because
 * sent as a pasted key it would make Proxmox generate one that the dialog
 * would never show.
 */
export function pbsKeyTextError(text: string): string | null {
  const trimmed = text.trim();
  if (trimmed === "") return "Paste a key, or load one from a file.";
  let parsed: unknown;
  try {
    parsed = JSON.parse(trimmed);
  } catch {
    return "This is not JSON. Paste the whole key file.";
  }
  // A JSON array can never have a "data" member, so the `in` test refuses one
  // without a check of its own.
  if (typeof parsed !== "object" || parsed === null || !("data" in parsed)) {
    return 'This is not a Proxmox Backup Server key: it has no "data" field.';
  }
  return null;
}

/** Whether a choice can be submitted as it stands. */
export function pbsEncryptionReady(choice: PBSEncryptionChoice): boolean {
  return choice.mode !== "existing" || pbsKeyTextError(choice.keyText) === null;
}

export interface PBSEncryptionRequest {
  /** The value to send as params.encryption-key, if any. */
  param?: string;
  /** Whether to name encryption-key in the update's delete list. */
  remove: boolean;
}

/**
 * What a choice sends. Keep and none send nothing at all, so a storage's key
 * is only ever touched by an explicit choice.
 */
export function pbsEncryptionRequest(
  choice: PBSEncryptionChoice,
): PBSEncryptionRequest {
  switch (choice.mode) {
    case "autogen":
      return { param: "autogen", remove: false };
    case "existing":
      return { param: choice.keyText.trim(), remove: false };
    case "remove":
      return { remove: true };
    default:
      return { remove: false };
  }
}

/** The file a generated key is downloaded as. */
export function pbsKeyFileName(storage: string): string {
  return `${storage}-encryption-key.json`;
}
