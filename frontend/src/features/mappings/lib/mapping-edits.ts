import { ApiClientError } from "@/lib/api-client";

/*
 * What the cluster's mapping cards share about writing a mapping: every save,
 * removal and delete is a compare-and-swap on the digest of the kind's whole
 * config file (usb.cfg, pci.cfg), refused with 409 when the file changed since
 * the listing the operator was looking at.
 */

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
