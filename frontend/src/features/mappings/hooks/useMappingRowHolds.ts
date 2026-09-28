import { useState } from "react";
import type { WorkKind } from "../lib/mapping-edits";

/**
 * The rows of a mapping card that a write holds. A mapping's own row is held
 * from its write until the write has settled AND the listing has been read
 * again (`settled`): released on the write alone, the row would offer the old
 * listing's entries — a deleted mapping's Delete, an edit pinned to the digest
 * the write just changed — until the re-read landed. Every other row is held
 * too ("waiting"), since the write may have changed the digest they carry.
 *
 * Each mapping's hold is its own: the card's mutations are shared by every
 * row, so their own pending state would track only the latest call.
 */
export function useMappingRowHolds(settled: () => Promise<unknown>) {
  const [working, setWorking] = useState<ReadonlyMap<string, WorkKind>>(
    new Map(),
  );

  const startWork = (id: string, kind: WorkKind) => {
    setWorking((w) => new Map(w).set(id, kind));
  };
  const endWork = (id: string) => {
    setWorking((w) => {
      const next = new Map(w);
      next.delete(id);
      return next;
    });
  };

  /**
   * Holds mapping `id`'s row, as `kind`, until `write` and the re-read after
   * it are done. The re-read follows a failed write too: after a 409 the
   * listing is what is stale. `write` must resolve even when the write fails
   * — the caller reports the failure first, as a card's Remove does — and one
   * that rejects all the same is not swallowed here but still rejects, after
   * the re-read.
   */
  const holdRow = (id: string, kind: WorkKind, write: Promise<unknown>) => {
    startWork(id, kind);
    void write
      .finally(() => settled())
      .finally(() => {
        endWork(id);
      });
  };

  /** What mapping `id`'s row is held for, or undefined when it is free. */
  const workFor = (id: string): WorkKind | undefined =>
    working.get(id) ?? (working.size > 0 ? "waiting" : undefined);

  return { holdRow, workFor };
}
