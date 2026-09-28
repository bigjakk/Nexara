import { useState } from "react";
import type {
  QueryObserverResult,
  RefetchOptions,
} from "@tanstack/react-query";
import { isConflict, type Reread } from "../lib/mapping-edits";

/**
 * The mapping an edit dialog saves against: its entries and the digest they
 * were read with, pinned when the dialog opens. A refetch of the listing while
 * the dialog is open does not reach it — the digest must stay the one the
 * form's values came from, or the compare-and-swap would wave through the very
 * overwrite it exists to stop. Only a 409 this dialog was shown moves the pin,
 * and only to a re-read that succeeded: `onSaveError` re-reads the listing,
 * shows it, and pins the mapping as it now is.
 */
export function usePinnedMapping<M extends { id: string }>(
  initial: M,
  listing: {
    refetch: (options?: RefetchOptions) => Promise<QueryObserverResult<M[]>>;
  },
) {
  const [pinned, setPinned] = useState(initial);
  const [reread, setReread] = useState<Reread>("none");

  /** For a save of the pinned mapping that failed with `err`. */
  const onSaveError = (err: unknown) => {
    if (!isConflict(err)) return;
    // Re-read, show it, and pin it: without this the pin would conflict
    // forever. Not a retry — the operator checks the new state and saves
    // again, or not.
    setReread("reading");
    // Joins the re-read the mutation already started (its refreshAfterWrite)
    // rather than cancelling it for a second one: that one began after the
    // 409, so it is the read to pin.
    void listing.refetch({ cancelRefetch: false }).then((res) => {
      // isSuccess, not res.data: a failed refetch keeps the last good data,
      // which would pin a digest nobody was shown.
      if (!res.isSuccess) {
        setReread("failed");
        return;
      }
      // By the id it opened with: a re-read only ever pins the same mapping.
      const fresh = res.data.find((m) => m.id === initial.id);
      if (fresh === undefined) {
        setReread("gone");
        return;
      }
      setPinned(fresh);
      setReread("reloaded");
    });
  };

  return { pinned, reread, onSaveError };
}
