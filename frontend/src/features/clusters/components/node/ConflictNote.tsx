import type { ReactNode } from "react";

import type { NodeOptionsConflict } from "../../hooks/useNodeOptionsSave";

/**
 * What a 409 leaves under the form, and what happens next (see
 * useNodeOptionsSave). The alert above it says that the node changed; this says
 * what the dialog is doing about it, in the words that are true for it: it
 * reads the node again, and it moves its pin only when that read succeeded.
 *
 * `repinned` is what to say once the read did succeed. Only the dialog can say
 * it, because only the dialog knows what it shows and so what of the read is
 * worth the operator's attention. Like the other two notes it is a sentence or
 * two: it is a live region, and a screen reader reads all of it out.
 *
 * `details` is anything longer that goes with it, such as the notes now stored
 * on the node, which can run to 64 KiB. It is shown in the same state as
 * `repinned` and for the same reason, but OUTSIDE the live region: announcing
 * it would read the whole of it out the moment the re-read lands. The operator
 * reads it, when and as far as they choose, as an ordinary part of the form.
 */
export function ConflictNote({
  conflict,
  repinned,
  details,
}: {
  conflict: NodeOptionsConflict;
  repinned: ReactNode;
  details?: ReactNode;
}) {
  if (conflict === null) return null;
  return (
    // A plain wrapper, so that the two read as one note: it is the status div
    // inside that is the live region.
    <div className="space-y-1.5">
      <div role="status" className="space-y-1.5 text-sm text-muted-foreground">
        {conflict === "rereading" ? (
          <p>Reading this node's current configuration…</p>
        ) : conflict === "repinned" ? (
          repinned
        ) : (
          <p>
            Nexara could not re-read this node's configuration, so saving again
            will be refused again until it can.
          </p>
        )}
      </div>
      {conflict === "repinned" && details}
    </div>
  );
}
