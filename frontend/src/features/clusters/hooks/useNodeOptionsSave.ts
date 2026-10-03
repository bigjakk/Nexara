import { useLayoutEffect, useRef, useState } from "react";
import type { QueryObserverResult } from "@tanstack/react-query";
import { toast } from "sonner";

import { ApiClientError, sessionScope } from "@/lib/api-client";
import { describeError } from "@/lib/api-error";
import {
  useSetNodeOptions,
  type NodeOptionsChanges,
} from "../api/node-options-queries";

/**
 * What the dialog tells the operator about the digest it saves with, after a
 * save the node refused as stale (a 409).
 *
 *   rereading      the node's current configuration is being read again.
 *   repinned       it was, and the next save is made against it.
 *   reread-failed  it could not be, so the next save is made against the same
 *                  digest again and will be refused again.
 */
export type NodeOptionsConflict =
  null | "rereading" | "repinned" | "reread-failed";

/** What a request that never got an answer reads as: see describeError. */
const CONNECTION_FAILED =
  "The save request failed — check your connection and try again.";

/**
 * What the dialog says of a 409. Not the server's, which tells the operator to
 * "reload and try again": this dialog's fields do not reload. What it does about
 * the conflict — read the node again, move its pin — is for the note under the
 * form to say as it happens, not for this alert to promise.
 */
const STALE_DIGEST =
  "This node's configuration changed while this dialog was open.";

/**
 * The save of one open Edit Options or Edit Notes dialog: a compare-and-swap on
 * the node's config file, through the `digest` a read of it gave. That is an
 * opaque save token, not Proxmox's own digest: this code only holds it and
 * sends it back, and Nexara re-reads the node and compares it before Proxmox's
 * own check.
 *
 * The digest is pinned to the read the form was drawn from — `opened`, which
 * the card hands over as a snapshot and never changes under the dialog — and
 * is never fetched again on open, nor read from the card's live query at save
 * time. It covers the WHOLE file, ACME keys and all, and any refetch landing
 * while the dialog is open (a WebSocket reconnect invalidates every active
 * query, an ACME save invalidates the node) would otherwise swap it under
 * values that never move with it. The save would then carry a digest that
 * matches a version of the node the operator never saw, and the compare-and-swap
 * would wave through exactly the overwrite it exists to catch. Pinned, a node
 * that changed since the read answers 409, which is the point.
 *
 * After a 409 the dialog stays open with what the operator typed. The node's
 * current configuration is read again through the CARD's query (`reread`: the
 * dialog must not call useNodeOptions itself, since an observer that mounts
 * refetches stale data, which is a refetch on open), the card behind shows it,
 * and the pin moves to its digest — and only when that read succeeded: a failed
 * refetch RETAINS the last data, so a result that merely HAS data would move the
 * pin onto a digest belonging to a change the operator has not seen. Saving
 * again then writes the settings changed here over whatever the node holds for
 * them; that is the operator's call, and never an automatic retry, which would
 * perform the very overwrite the 409 prevented. The card sits behind the
 * dialog's overlay, where the operator cannot see what the read found, so the
 * latest read that SUCCEEDED is handed back (`latest`) for the dialog to say what
 * differs from what it opened with.
 *
 * A failure is shown in the dialog while it is open. TanStack runs the
 * callbacks given to mutate() only while the component is mounted and the
 * mutation has opted out of the global toast (errorsHandledLocally), so the
 * promise carries the outcome instead, and a failure that lands after the
 * dialog was dismissed is toasted, naming the node: it can arrive on another
 * page, or over another open dialog.
 *
 * Unless the session the save was made in has ended (sessionScope): a sign-out,
 * an expiry, or another user signing in. A toast raised after any of the three
 * is shown to whoever is signed in by then, with the node's name and the
 * server's words in it: what is dismissed when a session ends and when the next
 * begins (stores/session-reset.ts) is only what exists at the time. A save that
 * settles after its session ended does nothing at all: no toast, no state, no
 * re-read (a request that would go out as the next user, or as nobody), and no
 * onSaved.
 */
export function useNodeOptionsSave<TRead extends { digest?: string }>(a: {
  clusterId: string;
  nodeName: string;
  /** The read the form was drawn from; its digest is the pin. */
  opened: TRead;
  /** Names what failed in the toast: "options" or "notes". */
  subject: "options" | "notes";
  /** The card's `query.refetch`, of the query `opened` was read from. */
  reread: () => Promise<QueryObserverResult<TRead>>;
  /** The save went through and the dialog is still open. */
  onSaved: () => void;
}) {
  const mutation = useSetNodeOptions(a.clusterId, a.nodeName);
  const [pinned, setPinned] = useState(a.opened.digest);
  const [error, setError] = useState("");
  const [conflict, setConflict] = useState<NodeOptionsConflict>(null);
  // The latest re-read that succeeded. Kept until a newer one replaces it: a
  // later failure does not make the node any less different from what the
  // dialog opened with.
  const [latest, setLatest] = useState<TRead | null>(null);

  // Whether this dialog is still mounted. A layout effect, so that it clears in
  // the commit that removes the dialog and not in the passive flush after it: a
  // request settling in between would find the dialog still live and show its
  // failure in a form no one can see, or close whichever dialog was opened in
  // its place.
  const live = useRef(false);
  useLayoutEffect(() => {
    live.current = true;
    return () => {
      live.current = false;
    };
  }, []);

  // Set by save() itself, ahead of the render that shows `pending`. A second
  // submit in that gap would send the same digest again, and the server would
  // refuse it as stale because of the first: a conflict the operator caused.
  const sending = useRef(false);

  // The number of saves so far, read by what comes back from one. The dialogs
  // hold Save while a re-read is out, so for them a re-read is always the last
  // thing a save did; but the pin is moved here, and a re-read that belongs to a
  // save since superseded must neither move it nor put its note up over the
  // newer one's.
  const attempt = useRef(0);

  const save = (changes: NodeOptionsChanges) => {
    if (sending.current || Object.keys(changes).length === 0) return;
    sending.current = true;
    attempt.current += 1;
    const gen = attempt.current;
    // The session this save is made in, taken as the request is sent and checked
    // first by whatever settles it, ahead of `live`. `live` says whether this
    // dialog is still on screen; this says whether the session Save was pressed
    // in is still the current one — which the same person signing in again ends
    // too. Both have to hold: a failure toasted, or a node read again, for a
    // session that has ended is for nobody who is here.
    const ended = sessionScope();
    setError("");
    setConflict(null);
    // No digest to pin means one of two things. The node has no config file yet,
    // or an empty one, so there is nothing to compare against: this is then an
    // unconditional write, as every save was before the compare-and-swap, since
    // the check degrades rather than lock the operator out. Or the caller may
    // not write: the server gives the digest only to callers who can manage the
    // node, and refuses that caller's save anyway.
    const body = pinned ? { ...changes, digest: pinned } : changes;
    mutation.mutateAsync(body).then(
      () => {
        // Released whatever the session: the flag only holds Save shut, and a
        // dialog that is somehow still there must not be left unable to save.
        sending.current = false;
        if (ended()) return;
        // Not once dismissed: onSaved would close whichever dialog has been
        // opened in this one's place.
        if (live.current) a.onSaved();
      },
      (err: unknown) => {
        sending.current = false;
        // Before the toast below, which is for a dialog that has gone, not for a
        // session that has.
        if (ended()) return;
        const message = describeError(err) || CONNECTION_FAILED;
        if (!live.current) {
          // The server's own words here: with the dialog gone, "reload and try
          // again" is just what to do.
          toast.error(
            `Saving the ${a.subject} of ${a.nodeName} failed: ${message}`,
          );
          return;
        }
        const stale = err instanceof ApiClientError && err.status === 409;
        setError(stale ? STALE_DIGEST : message);
        if (!stale) return;
        setConflict("rereading");
        a.reread().then(
          (res) => {
            // Either this dialog is gone, and its state with it, or another
            // save has been made since: whatever this would pin is not for it.
            // Nor when the session ended while the node was being read.
            if (ended() || !live.current || gen !== attempt.current) return;
            // isSuccess, not res.data: see above.
            if (!res.isSuccess) {
              setConflict("reread-failed");
              return;
            }
            if (res.data.digest) setPinned(res.data.digest);
            setLatest(res.data);
            setConflict("repinned");
          },
          () => {
            if (!ended() && live.current && gen === attempt.current) {
              setConflict("reread-failed");
            }
          },
        );
      },
    );
  };

  return { save, pending: mutation.isPending, error, conflict, latest };
}
