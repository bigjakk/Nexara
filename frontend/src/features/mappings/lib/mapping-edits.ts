import { ApiClientError } from "@/lib/api-client";

/*
 * What the cluster's mapping cards share about reading and writing a mapping:
 * every save, removal and delete is a compare-and-swap on the digest of the
 * kind's whole config file (usb.cfg, pci.cfg), refused with 409 when the file
 * changed since the listing the operator was looking at; and the listing's
 * maps are read by their own keys (ownValue).
 */

/**
 * `record[key]` when the record itself holds the key, else undefined. A
 * listing's maps are parsed JSON, so a plain index would give a key named
 * "constructor", say, what every object inherits in place of "not listed".
 * Through Object.prototype.hasOwnProperty rather than Object.hasOwn, which is
 * newer than the browsers the build targets (vite.config.ts).
 */
export function ownValue<T>(
  record: Readonly<Record<string, T>>,
  key: string,
): T | undefined {
  return Object.prototype.hasOwnProperty.call(record, key)
    ? record[key]
    : undefined;
}

/** A remove, delete or save refused because the file changed since its read. */
export function isConflict(err: unknown): boolean {
  return err instanceof ApiClientError && err.status === 409;
}

/**
 * What a mapping's row is waiting on: its own write and the re-read of it, or
 * — "waiting" — another mapping's. The digest covers every mapping of the
 * kind, so any write outdates every row's; each row is held until the re-read
 * lands.
 */
export type WorkKind = "removing" | "deleting" | "saving" | "waiting";

/** Why a held row offers nothing, shown in it. */
export const workLabel: Record<WorkKind, string> = {
  removing: "Removing the entry…",
  deleting: "Deleting…",
  saving: "Saving…",
  waiting: "Reloading after a change…",
};

/** Where the re-read after a 409 stands, and what it found. */
export type Reread = "none" | "reading" | "reloaded" | "gone" | "failed";
